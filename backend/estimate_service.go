package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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
