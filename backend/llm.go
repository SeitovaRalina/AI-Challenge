package main

import (
	"aiwork/backend/internal/llmgateway"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Sentinel errors keep handler status classification stable.
var (
	ErrUpstreamAuth        = llmgateway.ErrAuth
	ErrUpstreamUnavailable = llmgateway.ErrUnavailable
	ErrInvalidOutput       = errors.New("litellm: invalid estimate output")
	ErrContextOverflow     = llmgateway.ErrContextOverflow
)

// LiteLLMClient handles chat protocol; llmgateway owns HTTP transport.
type LiteLLMClient struct {
	model   string
	gateway *llmgateway.Client
}

func NewLiteLLMClient(baseURL, apiKey, model string) *LiteLLMClient {
	return &LiteLLMClient{model: model, gateway: llmgateway.New(baseURL, apiKey)}
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
	body, err := c.gateway.Complete(ctx, reqBody)
	if err != nil {
		return nil, err
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
