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

// uncontrolledSystemPrompt intentionally leaves format, length, and stopping
// condition unspecified, so its output can be compared against controlledSystemPrompt.
const uncontrolledSystemPrompt = `You are a software task estimation assistant.
Given a development task description, discuss what it involves, likely risks,
and give a rough time estimate. Write your answer in Russian.`

// controlledSystemPrompt extends systemPrompt with an explicit item-count
// limit and an explicit termination instruction, for the day-2 format-control
// comparison. An API-level stop sequence was tried instead of the termination
// instruction but proved unreliable with this reasoning model: LiteLLM
// sometimes cut the response during the model's hidden reasoning phase
// (content came back null) when a literal "stop" string was set.
const controlledSystemPrompt = systemPrompt + `

Include at most 3 items in "risks" and at most 3 items in "assumptions".
Output only that JSON object and absolutely nothing else - no text before it,
no text after it. Stop generating the moment the closing brace is written.`

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
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// chatComplete sends a chat-completion request to LiteLLM and returns the raw
// message content, applying an optional max-token limit and stop sequences.
func (c *LiteLLMClient) chatComplete(ctx context.Context, messages []chatMessage, maxTokens int, stop []string) (string, error) {
	reqBody, err := json.Marshal(chatCompletionRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0.2,
		MaxTokens:   maxTokens,
		Stop:        stop,
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: reading response: %v", ErrUpstreamUnavailable, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("%w: status %d", ErrUpstreamAuth, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("%w: status %d: %s", ErrUpstreamUnavailable, resp.StatusCode, truncate(string(body), 300))
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return "", fmt.Errorf("%w: decoding completion: %v", ErrUpstreamUnavailable, err)
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("%w: no choices in completion", ErrInvalidOutput)
	}

	return completion.Choices[0].Message.Content, nil
}

// Estimate sends the task to LiteLLM and returns a validated structured estimate.
func (c *LiteLLMClient) Estimate(ctx context.Context, task string) (*EstimateResponse, error) {
	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task},
	}, 0, nil)
	if err != nil {
		return nil, err
	}

	cleaned := stripCodeFences(content)

	var estimate EstimateResponse
	if err := json.Unmarshal([]byte(cleaned), &estimate); err != nil {
		return nil, fmt.Errorf("%w: model did not return valid JSON: %v", ErrInvalidOutput, err)
	}
	if err := estimate.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}

	return &estimate, nil
}

// CompareFormats sends the same task to LiteLLM twice — once with no response-
// format constraints, once with an explicit format, length limit, and stop
// condition — so the two responses can be compared side by side.
func (c *LiteLLMClient) CompareFormats(ctx context.Context, task string) (*CompareResponse, error) {
	uncontrolledStart := time.Now()
	uncontrolledText, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: uncontrolledSystemPrompt},
		{Role: "user", Content: task},
	}, 0, nil)
	if err != nil {
		return nil, err
	}
	uncontrolledLatency := time.Since(uncontrolledStart).Milliseconds()

	controlledStart := time.Now()
	controlledContent, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: controlledSystemPrompt},
		{Role: "user", Content: task},
	}, 1000, nil)
	if err != nil {
		return nil, err
	}
	controlledLatency := time.Since(controlledStart).Milliseconds()

	cleaned := stripCodeFences(controlledContent)

	var estimate EstimateResponse
	if err := json.Unmarshal([]byte(cleaned), &estimate); err != nil {
		return nil, fmt.Errorf("%w: model did not return valid JSON: %v", ErrInvalidOutput, err)
	}
	if err := estimate.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}

	return &CompareResponse{
		Task: task,
		Uncontrolled: RawResult{
			Text:        strings.TrimSpace(uncontrolledText),
			LengthChars: len([]rune(strings.TrimSpace(uncontrolledText))),
			LatencyMs:   uncontrolledLatency,
		},
		Controlled: ControlledResult{
			Estimate:    estimate,
			LengthChars: len([]rune(cleaned)),
			LatencyMs:   controlledLatency,
		},
	}, nil
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
