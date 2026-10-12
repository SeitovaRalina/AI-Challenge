package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

type providerComparisonRequest struct {
	Question string        `json:"question"`
	Strategy ChunkStrategy `json:"strategy"`
	Rerank   bool          `json:"rerank"`
	Rewrite  bool          `json:"rewrite"`
	MinScore float64       `json:"min_score"`
}

type providerGeneration struct {
	Answer     *RagAnswer `json:"answer,omitempty"`
	DurationMS int64      `json:"duration_ms"`
	Error      string     `json:"error,omitempty"`
}

type providerComparison struct {
	Retrieved   []RetrievedChunk   `json:"retrieved"`
	RetrievalMS int64              `json:"retrieval_ms"`
	Local       providerGeneration `json:"local"`
	Cloud       providerGeneration `json:"cloud"`
}

// compareProvidersHandler uses local retrieval once, then supplies the same
// prepared chunks to both generators. One provider's failure is inline.
func compareProvidersHandler(agent *Agent, ollama *OllamaClient, ragStore *RagStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req providerComparisonRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		req.Question = strings.TrimSpace(req.Question)
		if req.Question == "" || (req.Strategy != ChunkStrategyFixed && req.Strategy != ChunkStrategyStructural) || req.MinScore < 0 || req.MinScore > 1 {
			writeError(w, http.StatusBadRequest, "нужны question, strategy (fixed_size или structural) и min_score от 0 до 1")
			return
		}
		idx, err := ragStore.Load()
		if err != nil {
			log.Printf("rag: load index failed: %v", err)
			writeError(w, http.StatusInternalServerError, "не удалось прочитать индекс")
			return
		}
		if idx == nil {
			writeError(w, http.StatusConflict, "индекс ещё не построен")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
		defer cancel()
		local, err := agent.providerClient(ProviderOllama)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		started := time.Now()
		prepared, err := prepareRAG(ctx, local, ollama, idx, req.Question, req.Strategy, defaultTopK, RagOptions{Rerank: req.Rerank, Rewrite: req.Rewrite, MinScore: req.MinScore})
		if err != nil {
			writeRagQueryError(w, err)
			return
		}
		result := providerComparison{Retrieved: prepared.answer.Retrieved, RetrievalMS: time.Since(started).Milliseconds()}
		generate := func(client *LiteLLMClient) providerGeneration {
			started := time.Now()
			answer, err := generateRAG(ctx, client, req.Question, prepared)
			elapsed := time.Since(started).Milliseconds()
			if err != nil {
				return providerGeneration{DurationMS: elapsed, Error: err.Error()}
			}
			return providerGeneration{Answer: &answer, DurationMS: elapsed}
		}
		result.Local = generate(local)
		cloud, err := agent.providerClient(ProviderLiteLLM)
		if err != nil {
			result.Cloud = providerGeneration{Error: "LiteLLM не настроен"}
		} else {
			result.Cloud = generate(cloud)
		}
		writeJSON(w, http.StatusOK, result)
	}
}
