package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// defaultTopK is how many chunks ground a RAG answer — enough that a
// multi-session question (eval questions 7/9) has room to pull from more
// than one source, small enough that the prompt stays short.
const defaultTopK = 5

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

// AnswerRAG embeds question via Ollama, retrieves top-K chunks of
// strategy from idx, and answers grounded in them via LiteLLM.
func AnswerRAG(ctx context.Context, client *LiteLLMClient, ollama *OllamaClient, idx *RagIndex, question string, strategy ChunkStrategy, topK int) (RagAnswer, error) {
	vectors, err := ollama.Embeddings(ctx, []string{question})
	if err != nil {
		return RagAnswer{}, err
	}

	scored := retrieveTopK(idx.Chunks, strategy, vectors[0], topK)
	answer, err := client.chatComplete(ctx, buildRagPrompt(question, scored), 0.2, 0, nil)
	if err != nil {
		return RagAnswer{}, err
	}

	return RagAnswer{Mode: "rag", Strategy: strategy, Answer: answer, Retrieved: toRetrievedChunks(scored)}, nil
}

// Query dispatches to AnswerRAG or AnswerNoRAG — the single entry point
// POST /api/rag/query calls.
func Query(ctx context.Context, client *LiteLLMClient, ollama *OllamaClient, idx *RagIndex, question, mode string, strategy ChunkStrategy, topK int) (RagAnswer, error) {
	if mode == "no_rag" {
		answer, err := AnswerNoRAG(ctx, client, question)
		if err != nil {
			return RagAnswer{}, err
		}
		return RagAnswer{Mode: "no_rag", Answer: answer}, nil
	}
	return AnswerRAG(ctx, client, ollama, idx, question, strategy, topK)
}
