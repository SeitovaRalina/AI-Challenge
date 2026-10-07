package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// callWithRetry retries fn once after a short pause. Seen directly
// 2026-10-04: a 20-call eval run (no RAG + RAG per question, sequential)
// hit a transient "сервис LLM сейчас недоступен" after ~7 minutes and lost
// the whole run — a single flaky gateway call must not void everything
// before it.
func callWithRetry[T any](fn func() (T, error)) (T, error) {
	v, err := fn()
	if err == nil {
		return v, nil
	}
	time.Sleep(3 * time.Second)
	return fn()
}

// loadEvalQuestions reads the day-22 control-question set — a static,
// hand-written file (backend/data/rag_eval.json, gitignored with the rest
// of data/), not something the backend ever writes itself.
func loadEvalQuestions(path string) ([]EvalQuestion, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("файл с контрольными вопросами не найден: %s", path)
	}
	if err != nil {
		return nil, err
	}
	var questions []EvalQuestion
	if err := json.Unmarshal(data, &questions); err != nil {
		return nil, err
	}
	return questions, nil
}

// expectedSourceHit reports whether any retrieved chunk's session matches
// one of q's ExpectedSources, and whether that check even applies (it
// doesn't for aggregate/out-of-corpus questions with no ExpectedSources).
func expectedSourceHit(q EvalQuestion, retrieved []RetrievedChunk) (hit, applicable bool) {
	if len(q.ExpectedSources) == 0 {
		return false, false
	}
	expected := make(map[string]bool, len(q.ExpectedSources))
	for _, id := range q.ExpectedSources {
		expected[id] = true
	}
	for _, r := range retrieved {
		if expected[r.SessionID] {
			return true, true
		}
	}
	return false, true
}

// EvalProgress is one step of a RunEval call — reported via onProgress so
// a multi-minute run can show real progress instead of a bare spinner.
type EvalProgress struct {
	Step     int    `json:"step"`
	Total    int    `json:"total"`
	Question string `json:"question"`
	Stage    string `json:"stage"` // "no_rag" | "rag" | "rag_improved"
}

// RunEval answers every control question two ways — always two calls per
// question, which pair depends on opts:
//   - opts disabled (day-22 shape): без RAG vs обычный RAG. Comparing
//     retrieval against nothing at all.
//   - opts.Enabled() (day-23 shape): обычный RAG vs RAG с opts applied
//     (rerank/rewrite/filter). The "без RAG" baseline isn't computed at
//     all here — once you're testing whether rerank/filter helped, the
//     no-context answer is no longer the relevant comparison (user
//     feedback: "тебе уже по факту не нужен ответ без раг").
//
// A question whose call still fails after retry gets an inline error
// string instead of aborting the rest of the batch. onProgress may be nil
// (the plain, non-streaming endpoint doesn't report).
func RunEval(ctx context.Context, client *LiteLLMClient, ollama *OllamaClient, idx *RagIndex, questions []EvalQuestion, strategy ChunkStrategy, opts RagOptions, onProgress func(EvalProgress)) ([]EvalQuestionResult, error) {
	improved := opts.Enabled()
	total := len(questions) * 2
	step := 0
	report := func(question, stage string) {
		step++
		if onProgress != nil {
			onProgress(EvalProgress{Step: step, Total: total, Question: question, Stage: stage})
		}
	}

	results := make([]EvalQuestionResult, 0, len(questions))
	for _, q := range questions {
		var noRag string
		if !improved {
			var err error
			noRag, err = callWithRetry(func() (string, error) { return AnswerNoRAG(ctx, client, q.Question) })
			if err != nil {
				noRag = fmt.Sprintf("[ошибка: %v]", err)
			}
			report(q.Question, "no_rag")
		}

		var ragAnswer string
		var retrieved []RetrievedChunk
		var citations []Citation
		var lowConfidence bool
		var hit, applicable bool
		rag, err := callWithRetry(func() (RagAnswer, error) {
			return AnswerRAG(ctx, client, ollama, idx, q.Question, strategy, defaultTopK, RagOptions{})
		})
		if err != nil {
			ragAnswer = fmt.Sprintf("[ошибка: %v]", err)
		} else {
			ragAnswer = rag.Answer
			retrieved = rag.Retrieved
			citations = rag.Citations
			lowConfidence = rag.LowConfidence
			hit, applicable = expectedSourceHit(q, rag.Retrieved)
		}
		report(q.Question, "rag")

		var improvedAnswer string
		var improvedRetrieved []RetrievedChunk
		var improvedCitations []Citation
		var improvedLowConfidence bool
		if improved {
			improvedRag, err := callWithRetry(func() (RagAnswer, error) {
				return AnswerRAG(ctx, client, ollama, idx, q.Question, strategy, defaultTopK, opts)
			})
			if err != nil {
				improvedAnswer = fmt.Sprintf("[ошибка: %v]", err)
			} else {
				improvedAnswer = improvedRag.Answer
				improvedRetrieved = improvedRag.Retrieved
				improvedCitations = improvedRag.Citations
				improvedLowConfidence = improvedRag.LowConfidence
			}
			report(q.Question, "rag_improved")
		}

		results = append(results, EvalQuestionResult{
			EvalQuestion:          q,
			NoRagAnswer:           noRag,
			RagAnswer:             ragAnswer,
			Retrieved:             retrieved,
			Citations:             citations,
			LowConfidence:         lowConfidence,
			ExpectedSourceHit:     hit,
			ExpectedSourceCheck:   applicable,
			ImprovedRagAnswer:     improvedAnswer,
			ImprovedRetrieved:     improvedRetrieved,
			ImprovedCitations:     improvedCitations,
			ImprovedLowConfidence: improvedLowConfidence,
		})
	}
	return results, nil
}

// RunRetrievalEval is the secondary, no-LLM comparison: for every
// applicable question, does top-K retrieval surface the expected source
// under EACH strategy? This is the day-21-leftover "which chunking
// strategy is actually better" question, moved here because it needs
// retrieval (a day-22 mechanic) to answer honestly.
func RunRetrievalEval(ctx context.Context, ollama *OllamaClient, idx *RagIndex, questions []EvalQuestion) (*RetrievalEvalResult, error) {
	strategies := []ChunkStrategy{ChunkStrategyFixed, ChunkStrategyStructural}
	hits := map[ChunkStrategy]int{}
	totals := map[ChunkStrategy]int{}

	questionResults := make([]RetrievalQuestionResult, 0, len(questions))
	for _, q := range questions {
		row := RetrievalQuestionResult{Question: q.Question, Hits: map[ChunkStrategy]bool{}}
		if len(q.ExpectedSources) == 0 {
			questionResults = append(questionResults, row)
			continue
		}
		row.Checked = true

		vectors, err := ollama.Embeddings(ctx, []string{q.Question})
		if err != nil {
			return nil, fmt.Errorf("эмбеддинг вопроса %q: %w", q.Question, err)
		}

		for _, strategy := range strategies {
			scored := retrieveTopK(idx.Chunks, strategy, vectors[0], defaultTopK)
			hit, _ := expectedSourceHit(q, toRetrievedChunks(scored))
			row.Hits[strategy] = hit
			totals[strategy]++
			if hit {
				hits[strategy]++
			}
		}
		questionResults = append(questionResults, row)
	}

	stats := make([]RetrievalHitRate, 0, len(strategies))
	for _, strategy := range strategies {
		rate := 0.0
		if totals[strategy] > 0 {
			rate = float64(hits[strategy]) / float64(totals[strategy])
		}
		stats = append(stats, RetrievalHitRate{Strategy: strategy, Hits: hits[strategy], Total: totals[strategy], HitRate: rate})
	}

	return &RetrievalEvalResult{Questions: questionResults, Strategies: stats}, nil
}
