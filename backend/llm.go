package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Sentinel errors classified by the HTTP handler into application-level status codes.
var (
	ErrUpstreamAuth        = errors.New("litellm: authentication failed")
	ErrUpstreamUnavailable = errors.New("litellm: service unavailable")
	ErrInvalidOutput       = errors.New("litellm: invalid estimate output")
)

const systemPrompt = `You are a preliminary software task estimation assistant.

Analyze the software-development task described by the user and respond with
ONLY a single JSON object, no markdown code fences, no commentary before or
after it, matching exactly this schema:

{
  "summary": "one or two sentence summary of the task",
  "category": "short category label, e.g. 'mobile app maintenance', 'backend feature', 'bug fix'",
  "complexity": "low" | "medium" | "high",
  "estimated_hours_min": number,
  "estimated_hours_max": number,
  "risks": ["short risk statements"],
  "assumptions": ["short assumption statements"]
}

Write all natural-language string values ("summary", "category", entries in
"risks" and "assumptions") in Russian. Keep the JSON keys and the
"complexity" enum values themselves exactly as "low", "medium", or "high"
(never translate the enum values).

This is only a preliminary, generic estimate with no knowledge of any specific
person's work history. Never claim or imply that the estimate is personalized,
based on the user's previous work, or based on historical averages.`

// LiteLLMClient talks to the company LiteLLM gateway using its OpenAI-compatible
// chat completions endpoint.
type LiteLLMClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewLiteLLMClient(baseURL, apiKey, model string) *LiteLLMClient {
	return &LiteLLMClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// Estimate sends the task to LiteLLM and returns a validated structured estimate.
func (c *LiteLLMClient) Estimate(ctx context.Context, task string) (*EstimateResponse, error) {
	reqBody, err := json.Marshal(chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: task},
		},
		Temperature: 0.2,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: reading response: %v", ErrUpstreamUnavailable, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: status %d", ErrUpstreamAuth, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: status %d: %s", ErrUpstreamUnavailable, resp.StatusCode, truncate(string(body), 300))
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return nil, fmt.Errorf("%w: decoding completion: %v", ErrUpstreamUnavailable, err)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("%w: no choices in completion", ErrInvalidOutput)
	}

	content := stripCodeFences(completion.Choices[0].Message.Content)

	var estimate EstimateResponse
	if err := json.Unmarshal([]byte(content), &estimate); err != nil {
		return nil, fmt.Errorf("%w: model did not return valid JSON: %v", ErrInvalidOutput, err)
	}
	if err := estimate.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}

	return &estimate, nil
}

// stripCodeFences defensively removes ```json ... ``` wrapping some models add
// despite being instructed to return raw JSON.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if nl := strings.IndexByte(s, '\n'); nl != -1 {
		firstLine := strings.TrimSpace(s[:nl])
		if firstLine == "json" || firstLine == "" {
			s = s[nl+1:]
		}
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
