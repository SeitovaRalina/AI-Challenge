package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Sentinel errors classified by the HTTP handler into application-level status codes.
var (
	ErrUpstreamAuth        = errors.New("litellm: authentication failed")
	ErrUpstreamUnavailable = errors.New("litellm: service unavailable")
	ErrInvalidOutput       = errors.New("litellm: invalid estimate output")
	ErrContextOverflow     = errors.New("litellm: context length exceeded")
)

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
	// ToolCalls is set on an assistant message that asks for tool calls
	// (day 17); ToolCallID on the "tool" message answering one of them.
	ToolCalls  []llmToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// llmToolCall is one OpenAI-format function call the model asked for.
type llmToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// llmTool is one OpenAI-format function definition offered to the model.
type llmTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}

type chatCompletionRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
	Stop           []string      `json:"stop,omitempty"`
	Tools          []llmTool     `json:"tools,omitempty"`
	ToolChoice     string        `json:"tool_choice,omitempty"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
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
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatCompletionUsage `json:"usage"`
}

// doChatCompletion sends a chat-completion request to LiteLLM for the given
// model and returns the full parsed response (message content plus token
// usage and cost), so callers that only need the text (chatComplete) and
// callers that also need usage (the day-5 model comparison) share one
// request/response implementation.
func (c *LiteLLMClient) doChatCompletion(ctx context.Context, model string, messages []chatMessage, temperature float64, maxTokens int, stop []string) (*chatCompletionResponse, error) {
	return c.complete(ctx, chatCompletionRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		Stop:        stop,
	})
}

// doChatCompletionWithTools is doChatCompletion with function tools offered
// to the model — the response may then carry tool_calls instead of a final
// answer (finish_reason "tool_calls").
// toolChoice "none" still describes the tools (required once the history
// contains tool calls) but forbids calling them; "" leaves it to the model.
func (c *LiteLLMClient) doChatCompletionWithTools(ctx context.Context, messages []chatMessage, temperature float64, maxTokens int, tools []llmTool, toolChoice string, jsonMode bool) (*chatCompletionResponse, error) {
	req := chatCompletionRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		Tools:       tools,
	}
	if len(tools) > 0 && toolChoice != "" {
		req.ToolChoice = toolChoice
	}
	if jsonMode {
		req.ResponseFormat = &struct {
			Type string `json:"type"`
		}{Type: "json_object"}
	}
	return c.complete(ctx, req)
}

func (c *LiteLLMClient) complete(ctx context.Context, req chatCompletionRequest) (*chatCompletionResponse, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		completion, retry, err := c.completeAttempt(ctx, reqBody)
		if !retry || attempt > 0 {
			return completion, err
		}
		log.Printf("litellm: temporary gateway error, retrying completion once: %v", err)
		timer := time.NewTimer(750 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("%w: retry loop exhausted", ErrUpstreamUnavailable)
}

// completeAttempt builds a fresh HTTP request for each attempt. A transient
// gateway response can be retried without reusing a consumed request body.
func (c *LiteLLMClient) completeAttempt(ctx context.Context, reqBody []byte) (*chatCompletionResponse, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("%w: reading response: %v", ErrUpstreamUnavailable, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, false, fmt.Errorf("%w: status %d", ErrUpstreamAuth, resp.StatusCode)
	case resp.StatusCode != http.StatusOK && isContextOverflow(string(body)):
		return nil, false, fmt.Errorf("%w: status %d: %s", ErrContextOverflow, resp.StatusCode, truncate(string(body), 300))
	case resp.StatusCode != http.StatusOK:
		retry := resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout
		return nil, retry, fmt.Errorf("%w: status %d: %s", ErrUpstreamUnavailable, resp.StatusCode, truncate(string(body), 300))
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return nil, false, fmt.Errorf("%w: decoding completion: %v", ErrUpstreamUnavailable, err)
	}
	if len(completion.Choices) == 0 {
		return nil, false, fmt.Errorf("%w: no choices in completion", ErrInvalidOutput)
	}

	return &completion, false, nil
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
// despite being instructed to return raw JSON, and strips any prose a model
// prepended or appended around the JSON object itself (e.g. "Конечно, вот
// оценка: {...}") by slicing from the first '{' to the matching last '}'.
func stripCodeFences(s string) string {
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
	if !strings.HasPrefix(s, "{") {
		if start := strings.IndexByte(s, '{'); start != -1 {
			if end := strings.LastIndexByte(s, '}'); end > start {
				s = s[start : end+1]
			}
		}
	}
	return s
}

// isContextOverflow recognizes the handful of phrasings LiteLLM/upstream
// providers use to report that a request's prompt exceeded the model's
// context window, so that case can be surfaced distinctly from a generic
// upstream failure.
func isContextOverflow(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "maximum context length") ||
		strings.Contains(lower, "context window")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
