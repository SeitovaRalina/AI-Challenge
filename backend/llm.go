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

// buildControlledSystemPrompt extends systemPrompt with an explicit item-count
// limit and, optionally, an explicit termination instruction, for the day-2
// format-control comparison. An API-level stop sequence was tried instead of
// the termination instruction but proved unreliable with this reasoning
// model: LiteLLM sometimes cut the response during the model's hidden
// reasoning phase (content came back null) when a literal "stop" string was set.
func buildControlledSystemPrompt(maxItems int, useStopInstruction bool) string {
	prompt := fmt.Sprintf("%s\n\nInclude at most %d items in \"risks\" and at most %d items in \"assumptions\".",
		systemPrompt, maxItems, maxItems)
	if useStopInstruction {
		prompt += `
Output only that JSON object and absolutely nothing else - no text before it,
no text after it. Stop generating the moment the closing brace is written.`
	}
	return prompt
}

// LiteLLMClient talks to the company LiteLLM gateway using its OpenAI-compatible
// chat completions endpoint.
type LiteLLMClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewLiteLLMClient(baseURL, apiKey, model string) *LiteLLMClient {
	// httpClient.Timeout is kept above every handler's request context
	// timeout (currently 180s, for the multi-call reasoning/temperature/
	// model endpoints) so a slow upstream call fails with that context's
	// clean error instead of this client-wide timeout firing first.
	return &LiteLLMClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 220 * time.Second},
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

// chatCompletionUsage is the token/cost accounting the LiteLLM gateway
// includes on every completion. Cost is a pointer since some upstream
// providers may not report it.
type chatCompletionUsage struct {
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	Cost             *float64 `json:"cost"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage *chatCompletionUsage `json:"usage"`
}

// doChatCompletion sends a chat-completion request to LiteLLM for the given
// model and returns the full parsed response (message content plus token
// usage and cost), so callers that only need the text (chatComplete) and
// callers that also need usage (the day-5 model comparison) share one
// request/response implementation.
func (c *LiteLLMClient) doChatCompletion(ctx context.Context, model string, messages []chatMessage, temperature float64, maxTokens int, stop []string) (*chatCompletionResponse, error) {
	reqBody, err := json.Marshal(chatCompletionRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		Stop:        stop,
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

	return &completion, nil
}

// chatComplete sends a chat-completion request to LiteLLM using the app's
// configured default model, and returns the raw message content.
func (c *LiteLLMClient) chatComplete(ctx context.Context, messages []chatMessage, temperature float64, maxTokens int, stop []string) (string, error) {
	completion, err := c.doChatCompletion(ctx, c.model, messages, temperature, maxTokens, stop)
	if err != nil {
		return "", err
	}
	return completion.Choices[0].Message.Content, nil
}

// parseEstimateJSON strips optional code fences, unmarshals, and validates a
// model response against the EstimateResponse schema. It returns the cleaned
// JSON text alongside the parsed estimate so callers can report its length.
func parseEstimateJSON(raw string) (*EstimateResponse, string, error) {
	cleaned := stripCodeFences(raw)

	var estimate EstimateResponse
	if err := json.Unmarshal([]byte(cleaned), &estimate); err != nil {
		return nil, cleaned, fmt.Errorf("%w: model did not return valid JSON: %v", ErrInvalidOutput, err)
	}
	if err := estimate.Validate(); err != nil {
		return nil, cleaned, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}

	return &estimate, cleaned, nil
}

// Estimate sends the task to LiteLLM and returns a validated structured estimate.
func (c *LiteLLMClient) Estimate(ctx context.Context, task string) (*EstimateResponse, error) {
	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}

	estimate, _, err := parseEstimateJSON(content)
	if err != nil {
		return nil, err
	}

	return estimate, nil
}

// CompareOptions controls the "controlled" side of CompareFormats. The
// "uncontrolled" side stays fixed, so it remains a meaningful baseline.
type CompareOptions struct {
	MaxTokens          int     `json:"max_tokens"`
	MaxItems           int     `json:"max_items"`
	Temperature        float64 `json:"temperature"`
	UseStopInstruction bool    `json:"use_stop_instruction"`
}

// UncontrolledEstimate sends the task with no response-format constraints.
// Its prompt and sampling settings are fixed, so calling it again for the
// same task is redundant work the caller can skip and reuse instead.
func (c *LiteLLMClient) UncontrolledEstimate(ctx context.Context, task string) (*RawResult, error) {
	start := time.Now()
	text, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: uncontrolledSystemPrompt},
		{Role: "user", Content: task},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(text)

	return &RawResult{
		Text:        trimmed,
		LengthChars: len([]rune(trimmed)),
		LatencyMs:   time.Since(start).Milliseconds(),
	}, nil
}

// ControlledEstimate sends the task with an explicit format, length limit,
// and (optionally) stop condition, per opts.
func (c *LiteLLMClient) ControlledEstimate(ctx context.Context, task string, opts CompareOptions) (*ControlledResult, error) {
	start := time.Now()
	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: buildControlledSystemPrompt(opts.MaxItems, opts.UseStopInstruction)},
		{Role: "user", Content: task},
	}, opts.Temperature, opts.MaxTokens, nil)
	if err != nil {
		return nil, err
	}
	latency := time.Since(start).Milliseconds()

	estimate, cleaned, err := parseEstimateJSON(content)
	if err != nil {
		return nil, err
	}

	return &ControlledResult{
		Estimate:    *estimate,
		LengthChars: len([]rune(cleaned)),
		LatencyMs:   latency,
		Options:     opts,
	}, nil
}

// CompareFormats sends the same task to LiteLLM twice — once with no response-
// format constraints, once with an explicit format, length limit, and stop
// condition — so the two responses can be compared side by side.
func (c *LiteLLMClient) CompareFormats(ctx context.Context, task string, opts CompareOptions) (*CompareResponse, error) {
	uncontrolled, err := c.UncontrolledEstimate(ctx, task)
	if err != nil {
		return nil, err
	}

	controlled, err := c.ControlledEstimate(ctx, task, opts)
	if err != nil {
		return nil, err
	}

	return &CompareResponse{
		Task:         task,
		Uncontrolled: *uncontrolled,
		Controlled:   *controlled,
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
