package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
)

// defaultTopK is how many chunks ground a RAG answer — enough that a
// multi-session question (eval questions 7/9) has room to pull from more
// than one source, small enough that the prompt stays short.
const defaultTopK = 5

// defaultCandidateK is how many chunks the cheap cosine pass pulls before
// reranking narrows them back down to defaultTopK — day 23's reranker
// only ever sees this pool, so a chunk that scores outside it (see the
// day-22 finding: the right chunk for "рефакторинг" ranked 435th of ~890)
// is still invisible to reranking. Rerank fixes ordering within a
// reasonable candidate pool, not a blind spot in that pool itself.
const defaultCandidateK = 20

// ragSystemPrompt instructs the model to answer only from the retrieved
// excerpts — day 22 has no mandatory-citation/"не знаю" enforcement yet
// (that's day 24), but asking it not to invent numbers keeps the RAG vs
// no-RAG comparison meaningful rather than both sides just guessing.
const ragSystemPrompt = `Ты отвечаешь на вопрос, используя приведённые ниже фрагменты из прошлых сессий оценки задач пользователя. Отвечай на русском языке, опираясь только на эти фрагменты — не придумывай цифры и факты, которых там нет. Если фрагментов недостаточно для ответа, прямо скажи об этом. Отвечай кратко и по существу — 2-4 предложения, без преамбул и длинных списков, если вопрос их прямо не требует.`

// noRagSystemPrompt is the honest baseline day 22 compares RAG against: a
// plain assistant with explicitly NO access to the user's history, so a
// difference between the two answers is attributable to retrieval, not to
// one side being told more about the product than the other. The same
// brevity instruction as ragSystemPrompt is deliberate — without it this
// side tends to compensate for having nothing concrete to say with long,
// generic elaboration, which makes the two columns look lopsided for
// reasons that have nothing to do with RAG itself.
const noRagSystemPrompt = `Ты обычный ассистент общего назначения. Отвечай на вопрос на русском языке, опираясь только на свои общие знания. У тебя НЕТ доступа к истории прошлых задач пользователя — не утверждай, что знаешь подробности его проектов. Отвечай кратко и по существу — 2-4 предложения, без преамбул и длинных списков, если вопрос их прямо не требует.`

// cosineSimilarity is the only ranking signal day 22 uses — no rerank yet
// (day 23).
func cosineSimilarity(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

type scoredChunk struct {
	Chunk
	Score float64
}

// retrieveTopK scores every chunk of one strategy against queryEmbedding
// and returns the k highest, best first.
func retrieveTopK(chunks []Chunk, strategy ChunkStrategy, queryEmbedding []float32, k int) []scoredChunk {
	scored := make([]scoredChunk, 0, len(chunks))
	for _, c := range chunks {
		if c.Strategy != strategy {
			continue
		}
		scored = append(scored, scoredChunk{Chunk: c, Score: cosineSimilarity(queryEmbedding, c.Embedding)})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > k {
		scored = scored[:k]
	}
	return scored
}

func toRetrievedChunks(scored []scoredChunk) []RetrievedChunk {
	out := make([]RetrievedChunk, len(scored))
	for i, s := range scored {
		out[i] = RetrievedChunk{SessionID: s.SessionID, Title: s.Title, Section: s.Section, ChunkID: s.ID, Score: s.Score, Text: s.Text}
	}
	return out
}

// buildRagPrompt numbers each retrieved chunk with its source session and
// section so the model's answer can (informally, ahead of day 24's
// enforced citations) refer back to "[2]" style markers.
func buildRagPrompt(question string, scored []scoredChunk) []chatMessage {
	var ctx strings.Builder
	for i, s := range scored {
		fmt.Fprintf(&ctx, "[%d] (сессия %s, %s)\n%s\n\n", i+1, s.SessionID, s.Section, s.Text)
	}
	return []chatMessage{
		{Role: "system", Content: ragSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("Фрагменты из прошлых задач:\n\n%sВопрос: %s", ctx.String(), question)},
	}
}

// AnswerNoRAG is the baseline side of the day-22 comparison: the same
// question, same LLM, zero retrieval.
func AnswerNoRAG(ctx context.Context, client *LiteLLMClient, question string) (string, error) {
	return client.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: noRagSystemPrompt},
		{Role: "user", Content: question},
	}, 0.2, 0, nil)
}

// RagOptions turns day 23's three additions on or off; the zero value
// reproduces day 22's exact behavior (topK by cosine, no filter, no
// rewrite, no rerank) so existing callers (RunRetrievalEval's hit-rate,
// day 22's demo) keep working unchanged.
type RagOptions struct {
	Rerank   bool    // widen to defaultCandidateK, rescore with an LLM judge, keep top topK
	Rewrite  bool    // reformulate the question before embedding it
	MinScore float64 // 0 = no threshold; drop any final chunk scoring below this
}

// Enabled reports whether any enhancement is actually on — the zero value
// (every field false/0) is "identical to baseline", so callers that build
// a baseline-vs-enhanced comparison (RunEval) know when there's a second
// answer worth computing at all.
func (o RagOptions) Enabled() bool {
	return o.Rerank || o.Rewrite || o.MinScore > 0
}

const rewriteSystemPrompt = `Переформулируй вопрос пользователя в короткий поисковый запрос для поиска по базе кратких технических заметок (оценки задач разработки: категория, сложность, часы, риски, допущения). Сохрани суть вопроса, но сформулируй ближе к стилю самих записей — коротко, по сути, без лишних слов и вопросительной формы. Ответь ТОЛЬКО переформулированным запросом, одной строкой, без кавычек и пояснений.`

// rewriteQuery asks the LLM for a search-friendlier restatement of
// question. A rewrite failure is not fatal to the caller — AnswerRAG
// falls back to the original question rather than losing the whole
// answer over this optional step.
func rewriteQuery(ctx context.Context, client *LiteLLMClient, question string) (string, error) {
	// maxTokens generous for the same reason as rerankLLM — see its comment.
	rewritten, err := client.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: rewriteSystemPrompt},
		{Role: "user", Content: question},
	}, 0.2, 8000, nil)
	if err != nil {
		return "", err
	}
	rewritten = strings.Trim(strings.TrimSpace(rewritten), `"'`)
	if rewritten == "" {
		return "", fmt.Errorf("rewrite: empty result")
	}
	return rewritten, nil
}

const rerankSystemPrompt = `Ты оцениваешь релевантность фрагментов текста по отношению к вопросу. Для каждого пронумерованного фрагмента ниже поставь оценку релевантности от 0 до 10 (10 — фрагмент прямо содержит ответ на вопрос, 0 — совсем не по теме). Ответь ТОЛЬКО JSON-массивом вида [{"index":1,"score":7},{"index":2,"score":0},...], без markdown и пояснений — по одному объекту на каждый фрагмент.`

type rerankScore struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

// stripJSONArrayFences is stripCodeFences (llm.go) for a JSON ARRAY
// response instead of an object — stripCodeFences slices from the first
// '{' to the last '}', which for a `[{...},{...}]` response strips the
// outer brackets too and leaves a comma-separated sequence of objects
// that isn't valid JSON on its own (seen directly: a well-formed rerank
// response failed to parse with "invalid character ',' after top-level
// value" because of exactly this).
func stripJSONArrayFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if nl := strings.IndexByte(s, '\n'); nl != -1 {
			firstLine := strings.TrimSpace(s[:nl])
			if firstLine == "json" || firstLine == "" {
				s = s[nl+1:]
			}
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	if !strings.HasPrefix(s, "[") {
		if start := strings.IndexByte(s, '['); start != -1 {
			if end := strings.LastIndexByte(s, ']'); end > start {
				s = s[start : end+1]
			}
		}
	}
	return s
}

// rerankLLM re-scores candidates against question with a single LLM call
// that sees the question and every candidate's text together — the thing
// a bi-encoder cosine pass structurally cannot do (query and chunk are
// embedded independently, never compared directly). Candidates missing
// from a malformed response keep their original cosine rank (sorted
// after any scored ones) rather than the whole call failing.
func rerankLLM(ctx context.Context, client *LiteLLMClient, question string, candidates []scoredChunk) ([]scoredChunk, error) {
	var prompt strings.Builder
	for i, c := range candidates {
		fmt.Fprintf(&prompt, "[%d] %s\n\n", i+1, c.Text)
	}
	// maxTokens generous, not small: this reasoning model's hidden
	// "thinking" tokens count against the same budget as the visible JSON
	// output, and 2000 was observed to sometimes leave nothing for the
	// actual answer (empty content) even though 16000 reliably worked.
	raw, err := client.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: rerankSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("Вопрос: %s\n\nФрагменты:\n\n%s", question, prompt.String())},
	}, 0.2, 16000, nil)
	if err != nil {
		return nil, err
	}

	var scores []rerankScore
	if err := json.Unmarshal([]byte(stripJSONArrayFences(raw)), &scores); err != nil {
		return nil, fmt.Errorf("rerank: invalid response: %w (raw: %s)", err, truncateForLog(raw))
	}
	byIndex := make(map[int]float64, len(scores))
	for _, s := range scores {
		byIndex[s.Index] = s.Score
	}

	reranked := make([]scoredChunk, len(candidates))
	copy(reranked, candidates)
	for i := range reranked {
		if score, ok := byIndex[i+1]; ok {
			reranked[i].Score = score
		}
	}
	sort.SliceStable(reranked, func(i, j int) bool { return reranked[i].Score > reranked[j].Score })
	return reranked, nil
}

// filterByThreshold drops every chunk scoring below minScore. minScore
// <= 0 is a no-op (day 22's unfiltered behavior) — can legitimately leave
// zero chunks when nothing clears the bar, which is the point: an honest
// "found nothing relevant" beats padding the prompt with noise.
func filterByThreshold(scored []scoredChunk, minScore float64) []scoredChunk {
	if minScore <= 0 {
		return scored
	}
	out := scored[:0:0]
	for _, c := range scored {
		if c.Score >= minScore {
			out = append(out, c)
		}
	}
	return out
}

// AnswerRAG embeds question via Ollama, retrieves and (per opts) rewrites/
// reranks/filters chunks of strategy from idx, and answers grounded in
// them via LiteLLM. With the zero-value RagOptions this is exactly day
// 22's behavior (topK by cosine, nothing more).
func AnswerRAG(ctx context.Context, client *LiteLLMClient, ollama *OllamaClient, idx *RagIndex, question string, strategy ChunkStrategy, topK int, opts RagOptions) (RagAnswer, error) {
	searchText := question
	var rewritten string
	if opts.Rewrite {
		// Retried: this reasoning model sometimes returns empty content for
		// a short structured-output call for no apparent reason (same call,
		// same prompt, different result) — seen directly, same quirk noted
		// in llm.go. One retry resolves it in practice.
		if r, err := callWithRetry(func() (string, error) { return rewriteQuery(ctx, client, question) }); err == nil {
			rewritten = r
			searchText = r
		} else {
			log.Printf("rag: query rewrite failed, falling back to original question: %v", err)
		}
	}

	vectors, err := ollama.Embeddings(ctx, []string{searchText})
	if err != nil {
		return RagAnswer{}, err
	}

	candidateK := topK
	if opts.Rerank && defaultCandidateK > candidateK {
		candidateK = defaultCandidateK
	}
	candidates := retrieveTopK(idx.Chunks, strategy, vectors[0], candidateK)
	candidateCount := len(candidates)

	ranked := candidates
	if opts.Rerank && len(candidates) > 0 {
		if r, err := callWithRetry(func() ([]scoredChunk, error) { return rerankLLM(ctx, client, question, candidates) }); err == nil {
			ranked = r
		} else {
			log.Printf("rag: rerank failed, falling back to cosine order: %v", err)
		}
	}
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}

	final := filterByThreshold(ranked, opts.MinScore)
	filteredCount := len(final)

	answer, err := client.chatComplete(ctx, buildRagPrompt(question, final), 0.2, 0, nil)
	if err != nil {
		return RagAnswer{}, err
	}

	result := RagAnswer{
		Mode:           "rag",
		Strategy:       strategy,
		Answer:         answer,
		Retrieved:      toRetrievedChunks(final),
		CandidateCount: candidateCount,
		FilteredCount:  filteredCount,
	}
	if rewritten != "" {
		result.RewrittenQuestion = rewritten
	}
	return result, nil
}

// Query dispatches to AnswerRAG or AnswerNoRAG — the single entry point
// POST /api/rag/query calls.
func Query(ctx context.Context, client *LiteLLMClient, ollama *OllamaClient, idx *RagIndex, question, mode string, strategy ChunkStrategy, topK int, opts RagOptions) (RagAnswer, error) {
	if mode == "no_rag" {
		answer, err := AnswerNoRAG(ctx, client, question)
		if err != nil {
			return RagAnswer{}, err
		}
		return RagAnswer{Mode: "no_rag", Answer: answer}, nil
	}
	return AnswerRAG(ctx, client, ollama, idx, question, strategy, topK, opts)
}
