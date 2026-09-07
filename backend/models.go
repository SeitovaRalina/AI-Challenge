package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// modelSystemPrompt reuses temperature.go's generic, domain-free-but-dev-work
// framing, so differences between models are caused by the model itself, not
// by task-specific prompt constraints.
const modelSystemPrompt = temperatureSystemPrompt

// modelQualitySystemPrompt asks the model to judge only the quality of the
// three responses — speed, token usage, and cost are measured directly from
// the gateway's own response (see ModelResult), never guessed by an LLM.
const modelQualitySystemPrompt = `You are comparing the same request answered by three models of increasing
capability tier — weak, medium, and strong — ordered by each model's
documented count of active MoE parameters (weak ~3B active, medium ~13B
active, strong ~49B active).

For EACH of the three responses, assess ONLY the quality of its answer in
Russian (2-4 sentences): how accurate, complete, and well-reasoned it is. Do
not comment on speed, length, or cost — those are measured separately and
shown elsewhere.

Then write an overall "summary" in Russian (4-6 sentences): how quality
actually differed across the three tiers for this request, general guidance
on when the extra capability (and cost) of a stronger model is worth it, and
an explicit recommendation for THIS specific task — which tier is the best
trade-off for it and why.

Respond with ONLY a single JSON object, no markdown code fences, no
commentary before or after it, matching exactly this schema:

{
  "quality": [
    {"tier": "weak", "quality": "..."},
    {"tier": "medium", "quality": "..."},
    {"tier": "strong", "quality": "..."}
  ],
  "summary": "..."
}

Keep the JSON keys and the "tier" values themselves exactly as given ("weak",
"medium", "strong"). Write every other string value in Russian.`

// modelResultAt sends the task to one specific model (bypassing the client's
// default configured model) and measures its real latency, token usage, and
// cost from the gateway's own response.
func (c *LiteLLMClient) modelResultAt(ctx context.Context, task string, info ModelInfo) (*ModelResult, error) {
	start := time.Now()
	completion, err := c.doChatCompletion(ctx, info.ModelID, []chatMessage{
		{Role: "system", Content: modelSystemPrompt},
		{Role: "user", Content: task},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}
	latency := time.Since(start).Milliseconds()

	result := &ModelResult{
		Tier:        info.Tier,
		ModelID:     info.ModelID,
		Name:        info.Name,
		Description: info.Description,
		DocsURL:     info.DocsURL,
		Text:        strings.TrimSpace(completion.Choices[0].Message.Content),
		LatencyMs:   latency,
	}
	if completion.Usage != nil {
		result.PromptTokens = completion.Usage.PromptTokens
		result.CompletionTokens = completion.Usage.CompletionTokens
		result.TotalTokens = completion.Usage.TotalTokens
		result.CostUsd = completion.Usage.Cost
	}
	return result, nil
}

// formatModelResultForQuality renders one model's response as plain text for
// the quality-judging prompt, labeled by tier so the judge can refer back to
// it without seeing which real model produced it.
func formatModelResultForQuality(r ModelResult) string {
	return fmt.Sprintf("%s (%s):\n%s", r.Tier, r.Name, r.Text)
}

// judgeModelQuality asks the app's default model to compare the three
// tiers' answer quality for the same task.
func (c *LiteLLMClient) judgeModelQuality(ctx context.Context, task string, results []ModelResult) (*ModelVerdict, error) {
	blocks := make([]string, len(results))
	for i, r := range results {
		blocks[i] = formatModelResultForQuality(r)
	}
	userMessage := task + "\n\n" + strings.Join(blocks, "\n\n")

	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: modelQualitySystemPrompt},
		{Role: "user", Content: userMessage},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}

	cleaned := stripCodeFences(content)
	var verdict ModelVerdict
	if err := json.Unmarshal([]byte(cleaned), &verdict); err != nil {
		return nil, fmt.Errorf("%w: quality judge did not return valid JSON: %v", ErrInvalidOutput, err)
	}
	if err := verdict.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}

	return &verdict, nil
}

// CompareModels solves the same task with three models of increasing
// capability tier in parallel, then asks the app's default model to judge
// answer quality across them, so the measured performance (speed, tokens,
// cost) and judged quality can be shown together.
func (c *LiteLLMClient) CompareModels(ctx context.Context, task string) (*ModelComparisonResponse, error) {
	results := make([]ModelResult, len(comparedModels))
	errs := make([]error, len(comparedModels))

	var wg sync.WaitGroup
	wg.Add(len(comparedModels))
	for i, info := range comparedModels {
		go func(i int, info ModelInfo) {
			defer wg.Done()
			result, err := c.modelResultAt(ctx, task, info)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = *result
		}(i, info)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	verdict, err := c.judgeModelQuality(ctx, task, results)
	if err != nil {
		return nil, err
	}

	return &ModelComparisonResponse{
		Task:    task,
		Results: results,
		Verdict: *verdict,
	}, nil
}
