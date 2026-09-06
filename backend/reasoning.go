package main

import (
	"context"
	"strings"
	"sync"
	"time"
)

// resultMarker separates a strategy's free-form reasoning text from the
// final JSON object it must still produce, so the two can be split reliably
// without guessing where prose ends and JSON begins.
const resultMarker = "<<<RESULT_JSON>>>"

const stepByStepSystemPrompt = systemPrompt + `

Before giving the JSON, think through the task step by step in Russian, as
plain reasoning text explaining your logic (not JSON, no code fences). When
you are done reasoning, output the line ` + resultMarker + ` by itself, then
nothing but the JSON object on the lines that follow.`

const expertPanelSystemPrompt = systemPrompt + `

Before the JSON, run a short internal panel discussion in Russian between
three personas: "Аналитик" (challenges scope and requirement ambiguity),
"Инженер" (assesses technical complexity and implementation approach), and
"Критик" (challenges optimistic assumptions and flags overlooked risks).
Give each persona two or three sentences, each on its own line prefixed with
its name, e.g. "Аналитик: ...". After all three have spoken, output the line
` + resultMarker + ` by itself, then nothing but a final JSON object that
reflects the panel's consensus.`

const metaPromptSystemPrompt = `You are an expert prompt engineer. A user wants an LLM to
produce an accurate software-development effort estimate for the task
described below. Write, in Russian, the single best prompt you would send an
LLM to get an accurate, well-reasoned estimate for that task.

Output ONLY that prompt text - no explanation, no commentary, no quotes or
code fences around it.`

// splitOnResultMarker separates reasoning text from the trailing JSON object
// a strategy was instructed to emit after resultMarker. If the model didn't
// include the marker, the whole content is treated as the JSON part so
// parseEstimateJSON can still attempt to recover it.
func splitOnResultMarker(content string) (reasoning string, jsonPart string) {
	idx := strings.Index(content, resultMarker)
	if idx == -1 {
		return "", content
	}
	return strings.TrimSpace(content[:idx]), strings.TrimSpace(content[idx+len(resultMarker):])
}

// directEstimate answers with no additional reasoning instructions — the
// same behavior as Estimate, wrapped as a ReasoningStep for comparison.
func (c *LiteLLMClient) directEstimate(ctx context.Context, task string) (*ReasoningStep, error) {
	start := time.Now()
	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}
	latency := time.Since(start).Milliseconds()

	estimate, cleaned, err := parseEstimateJSON(content)
	if err != nil {
		return nil, err
	}

	return &ReasoningStep{
		Estimate:    *estimate,
		LengthChars: len([]rune(cleaned)),
		LatencyMs:   latency,
	}, nil
}

// stepByStepEstimate asks the model to reason step by step before answering.
func (c *LiteLLMClient) stepByStepEstimate(ctx context.Context, task string) (*ReasoningStep, error) {
	start := time.Now()
	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: stepByStepSystemPrompt},
		{Role: "user", Content: task},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}
	latency := time.Since(start).Milliseconds()

	reasoning, jsonPart := splitOnResultMarker(content)
	estimate, cleaned, err := parseEstimateJSON(jsonPart)
	if err != nil {
		return nil, err
	}

	return &ReasoningStep{
		Reasoning:   reasoning,
		Estimate:    *estimate,
		LengthChars: len([]rune(cleaned)),
		LatencyMs:   latency,
	}, nil
}

// expertPanelEstimate asks the model to role-play a small panel of experts
// before synthesizing a final estimate.
func (c *LiteLLMClient) expertPanelEstimate(ctx context.Context, task string) (*ReasoningStep, error) {
	start := time.Now()
	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: expertPanelSystemPrompt},
		{Role: "user", Content: task},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}
	latency := time.Since(start).Milliseconds()

	reasoning, jsonPart := splitOnResultMarker(content)
	estimate, cleaned, err := parseEstimateJSON(jsonPart)
	if err != nil {
		return nil, err
	}

	return &ReasoningStep{
		Reasoning:   reasoning,
		Estimate:    *estimate,
		LengthChars: len([]rune(cleaned)),
		LatencyMs:   latency,
	}, nil
}

// metaPromptEstimate first asks the model to compose the prompt it thinks
// would best solve the task, then sends that generated prompt (plus the
// app's required JSON schema) as the actual estimation request.
func (c *LiteLLMClient) metaPromptEstimate(ctx context.Context, task string) (*ReasoningStep, error) {
	start := time.Now()

	generated, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: metaPromptSystemPrompt},
		{Role: "user", Content: task},
	}, 0.3, 0, nil)
	if err != nil {
		return nil, err
	}
	generatedPrompt := strings.TrimSpace(generated)

	finalSystem := generatedPrompt + "\n\n" + systemPrompt
	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: finalSystem},
		{Role: "user", Content: task},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}
	latency := time.Since(start).Milliseconds()

	estimate, cleaned, err := parseEstimateJSON(content)
	if err != nil {
		return nil, err
	}

	return &ReasoningStep{
		GeneratedPrompt: generatedPrompt,
		Estimate:        *estimate,
		LengthChars:     len([]rune(cleaned)),
		LatencyMs:       latency,
	}, nil
}

// CompareReasoning solves the same task with four reasoning strategies in
// parallel — direct, step-by-step, meta-prompting, and an expert panel — so
// their outputs can be compared side by side.
func (c *LiteLLMClient) CompareReasoning(ctx context.Context, task string) (*ReasoningResponse, error) {
	var (
		direct, stepByStep, metaPrompt, expertPanel *ReasoningStep
		directErr, stepErr, metaErr, expertErr      error
	)

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); direct, directErr = c.directEstimate(ctx, task) }()
	go func() { defer wg.Done(); stepByStep, stepErr = c.stepByStepEstimate(ctx, task) }()
	go func() { defer wg.Done(); metaPrompt, metaErr = c.metaPromptEstimate(ctx, task) }()
	go func() { defer wg.Done(); expertPanel, expertErr = c.expertPanelEstimate(ctx, task) }()
	wg.Wait()

	for _, err := range []error{directErr, stepErr, metaErr, expertErr} {
		if err != nil {
			return nil, err
		}
	}

	return &ReasoningResponse{
		Task:        task,
		Direct:      *direct,
		StepByStep:  *stepByStep,
		MetaPrompt:  *metaPrompt,
		ExpertPanel: *expertPanel,
	}, nil
}
