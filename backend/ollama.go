package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrOllamaUnavailable is returned when the local Ollama server can't be
// reached or errors out — day 21's indexing and day 22+'s query both fail
// fast with a clear message rather than silently falling back to anything,
// since a RAG answer built on a wrong/empty embedding is worse than no
// answer.
var ErrOllamaUnavailable = fmt.Errorf("ollama: embedding server unavailable")

// OllamaClient talks to a local Ollama instance for embeddings only — chat
// completions (rerank, query rewrite, the final answer) stay on the
// existing LiteLLMClient/company gateway, which has no embedding model
// configured for this product's key (verified 2026-10-04 against
// llm.effective.land: /v1/models lists chat models only, /v1/embeddings
// 400s with "Invalid model name").
type OllamaClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// 300s, not the usual 120s elsewhere in this codebase: a CPU-only Ollama
// instance (no GPU) embedding a batch of chunk texts measurably needs more
// than 120s under load — seen directly on 2026-10-04 (a 64-chunk batch hit
// "context deadline exceeded" at 120s).
func NewOllamaClient(baseURL, model string) *OllamaClient {
	return &OllamaClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		model:      model,
		httpClient: &http.Client{Timeout: 300 * time.Second},
	}
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error"`
}

// Embeddings calls Ollama's batch /api/embed endpoint once for every text in
// texts and returns one vector per input, in the same order. An empty texts
// slice is a no-op.
func (c *OllamaClient) Embeddings(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody, err := json.Marshal(ollamaEmbedRequest{Model: c.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOllamaUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: reading response: %v", ErrOllamaUnavailable, err)
	}

	var parsed ollamaEmbedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%w: decoding response: %v", ErrOllamaUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK || parsed.Error != "" {
		return nil, fmt.Errorf("%w: status %d: %s", ErrOllamaUnavailable, resp.StatusCode, parsed.Error)
	}
	if len(parsed.Embeddings) != len(texts) {
		return nil, fmt.Errorf("%w: expected %d embeddings, got %d", ErrOllamaUnavailable, len(texts), len(parsed.Embeddings))
	}

	return parsed.Embeddings, nil
}
