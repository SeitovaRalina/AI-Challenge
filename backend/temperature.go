package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// temperatureSystemPrompt keeps this app's software-development-work framing
// (unlike a generic assistant, it stays in character as a dev-work helper),
// but — unlike uncontrolledSystemPrompt's "estimate a rough time" framing —
// doesn't lock the request into hour estimation, since the day-4 challenge's
// one chosen task can be logical, algorithmic, or analytical (e.g. naming,
// architecture ideas), not necessarily "how long will this take".
const temperatureSystemPrompt = `You are a helpful assistant for software developers and their teams.
Answer the user's work-related request thoroughly and directly. Write your
answer in Russian.`

// temperatureAnalysisSystemPrompt asks the model to judge the same three
// unconstrained responses the day-4 challenge asks to compare — by accuracy,
// creativity and diversity — and to say which kinds of tasks each sampling
// temperature suits, in more depth than the single-verdict day-3 judge.
const temperatureAnalysisSystemPrompt = `You are analyzing the same response generated three times by
an LLM, once at each of these sampling temperatures: 0, 0.7, and 1.2. Lower
temperature makes output more deterministic and focused; higher temperature
makes it more varied and exploratory, at some risk to precision.

For EACH of the three responses, assess in Russian:
- "accuracy": how precise, correct, and on-topic it is (2-3 sentences)
- "creativity": how original or exploratory its ideas/phrasing are (2-3 sentences)
- "diversity": how varied its wording and the range of ideas it touches feels,
  and what that implies about how much answers would vary across repeated
  requests at that temperature (2-3 sentences)
- "best_for": 2-4 short Russian phrases naming concrete kinds of tasks this
  temperature suits well (e.g. "код-ревью и багфиксы", "мозговой штурм идей")

Then write an overall "summary" in Russian (4-6 sentences): first tie the
three together — how the responses actually differed here, and general
guidance for picking a temperature for a given kind of task — then end with
an explicit recommendation for THIS specific task: name which one of the
three temperatures suits it best and why, in concrete terms tied to what this
particular task needs (not a generic restatement of the guidance above).

Respond with ONLY a single JSON object, no markdown code fences, no
commentary before or after it, matching exactly this schema:

{
  "analysis": [
    {"temperature": 0, "accuracy": "...", "creativity": "...", "diversity": "...", "best_for": ["...", "..."]},
    {"temperature": 0.7, "accuracy": "...", "creativity": "...", "diversity": "...", "best_for": ["...", "..."]},
    {"temperature": 1.2, "accuracy": "...", "creativity": "...", "diversity": "...", "best_for": ["...", "..."]}
  ],
  "summary": "..."
}

Keep the JSON keys and the "temperature" numeric values themselves exactly as
given. Write every other string value in Russian.`

// rawResponseAtTemperature sends the task with a generic, domain-free system
// prompt at the given sampling temperature, so the three responses differ
// only in temperature.
func (c *LiteLLMClient) rawResponseAtTemperature(ctx context.Context, task string, temperature float64) (*TemperatureResult, error) {
	start := time.Now()
	text, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: temperatureSystemPrompt},
		{Role: "user", Content: task},
	}, temperature, 0, nil)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(text)

	return &TemperatureResult{
		Temperature: temperature,
		Text:        trimmed,
		LengthChars: len([]rune(trimmed)),
		LatencyMs:   time.Since(start).Milliseconds(),
	}, nil
}

// formatResultForAnalysis renders one temperature's response as plain text
// for the analysis prompt, labeled by temperature so the judge can refer
// back to it.
func formatResultForAnalysis(r TemperatureResult) string {
	return fmt.Sprintf("temperature %v:\n%s", r.Temperature, r.Text)
}

// analyzeTemperatures asks the model to compare the three temperature
// responses for the same task and break down accuracy, creativity and
// diversity for each, plus an overall takeaway.
func (c *LiteLLMClient) analyzeTemperatures(ctx context.Context, task string, results []TemperatureResult) (*TemperatureVerdict, error) {
	blocks := make([]string, len(results))
	for i, r := range results {
		blocks[i] = formatResultForAnalysis(r)
	}
	userMessage := task + "\n\n" + strings.Join(blocks, "\n\n")

	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: temperatureAnalysisSystemPrompt},
		{Role: "user", Content: userMessage},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}

	cleaned := stripCodeFences(content)
	var verdict TemperatureVerdict
	if err := json.Unmarshal([]byte(cleaned), &verdict); err != nil {
		return nil, fmt.Errorf("%w: analysis did not return valid JSON: %v", ErrInvalidOutput, err)
	}
	if err := verdict.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}

	return &verdict, nil
}

// CompareTemperatures solves the same task at three sampling temperatures in
// parallel — 0, 0.7, and 1.2 — then asks the model to judge accuracy,
// creativity and diversity for each, so the responses and that analysis can
// be shown together.
func (c *LiteLLMClient) CompareTemperatures(ctx context.Context, task string) (*TemperatureResponse, error) {
	results := make([]TemperatureResult, len(temperatureValues))
	errs := make([]error, len(temperatureValues))

	var wg sync.WaitGroup
	wg.Add(len(temperatureValues))
	for i, temperature := range temperatureValues {
		go func(i int, temperature float64) {
			defer wg.Done()
			result, err := c.rawResponseAtTemperature(ctx, task, temperature)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = *result
		}(i, temperature)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	verdict, err := c.analyzeTemperatures(ctx, task, results)
	if err != nil {
		return nil, err
	}

	return &TemperatureResponse{
		Task:    task,
		Results: results,
		Verdict: *verdict,
	}, nil
}
