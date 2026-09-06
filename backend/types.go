package main

import (
	"errors"
	"fmt"
	"strings"
)

// EstimateRequest is the payload accepted by POST /api/estimate.
type EstimateRequest struct {
	Task string `json:"task"`
}

// EstimateResponse is the structured estimate returned to the frontend.
// It mirrors the JSON schema requested from the LLM in llm.go.
type EstimateResponse struct {
	Summary           string   `json:"summary"`
	Category          string   `json:"category"`
	Complexity        string   `json:"complexity"`
	EstimatedHoursMin float64  `json:"estimated_hours_min"`
	EstimatedHoursMax float64  `json:"estimated_hours_max"`
	Risks             []string `json:"risks"`
	Assumptions       []string `json:"assumptions"`
}

// CompareRequest is the payload accepted by POST /api/compare. All fields but
// Task are optional and only affect the "controlled" side of the comparison.
type CompareRequest struct {
	Task               string   `json:"task"`
	MaxTokens          int      `json:"max_tokens"`
	MaxItems           int      `json:"max_items"`
	Temperature        *float64 `json:"temperature"`
	UseStopInstruction *bool    `json:"use_stop_instruction"`
}

const (
	defaultCompareMaxTokens   = 1000
	defaultCompareMaxItems    = 3
	defaultCompareTemperature = 0.2
)

// Options applies defaults for unset fields and clamps user-supplied values
// to safe ranges, so the frontend can send freeform numbers without the
// backend ever building an unreasonable or malformed LLM request.
func (r CompareRequest) Options() CompareOptions {
	maxTokens := r.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultCompareMaxTokens
	}
	maxTokens = clampInt(maxTokens, 200, 4000)

	maxItems := r.MaxItems
	if maxItems <= 0 {
		maxItems = defaultCompareMaxItems
	}
	maxItems = clampInt(maxItems, 1, 10)

	temperature := defaultCompareTemperature
	if r.Temperature != nil {
		temperature = *r.Temperature
	}
	temperature = clampFloat(temperature, 0, 1)

	useStopInstruction := true
	if r.UseStopInstruction != nil {
		useStopInstruction = *r.UseStopInstruction
	}

	return CompareOptions{
		MaxTokens:          maxTokens,
		MaxItems:           maxItems,
		Temperature:        temperature,
		UseStopInstruction: useStopInstruction,
	}
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// RawResult is the free-form, unconstrained LLM response for the day-2 comparison.
type RawResult struct {
	Text        string `json:"text"`
	LengthChars int    `json:"length_chars"`
	LatencyMs   int64  `json:"latency_ms"`
}

// ControlledResult is the constrained, structured LLM response for the day-2 comparison.
type ControlledResult struct {
	Estimate    EstimateResponse `json:"estimate"`
	LengthChars int              `json:"length_chars"`
	LatencyMs   int64            `json:"latency_ms"`
	Options     CompareOptions   `json:"options"`
}

// CompareResponse holds both variants of the same task sent to the LLM,
// one without response-format constraints and one with them.
type CompareResponse struct {
	Task         string           `json:"task"`
	Uncontrolled RawResult        `json:"uncontrolled"`
	Controlled   ControlledResult `json:"controlled"`
}

// ReasoningRequest is the payload accepted by POST /api/reasoning.
type ReasoningRequest struct {
	Task string `json:"task"`
}

// ReasoningStep is one reasoning strategy's result for the day-3 comparison.
// Reasoning holds the free-form reasoning text for strategies that produce
// one (step-by-step, expert panel); GeneratedPrompt holds the model-authored
// prompt for the meta-prompting strategy. Both are empty for the direct
// strategy, which has neither.
type ReasoningStep struct {
	Reasoning       string           `json:"reasoning,omitempty"`
	GeneratedPrompt string           `json:"generated_prompt,omitempty"`
	Estimate        EstimateResponse `json:"estimate"`
	LengthChars     int              `json:"length_chars"`
	LatencyMs       int64            `json:"latency_ms"`
}

// ReasoningResponse holds the same task solved via four reasoning
// strategies, so they can be compared side by side.
type ReasoningResponse struct {
	Task        string        `json:"task"`
	Direct      ReasoningStep `json:"direct"`
	StepByStep  ReasoningStep `json:"step_by_step"`
	MetaPrompt  ReasoningStep `json:"meta_prompt"`
	ExpertPanel ReasoningStep `json:"expert_panel"`
}

var validComplexity = map[string]bool{"low": true, "medium": true, "high": true}

// Validate rejects model output that doesn't satisfy the application's schema,
// so a malformed LLM response never reaches the frontend as if it were trusted data.
func (e EstimateResponse) Validate() error {
	if strings.TrimSpace(e.Summary) == "" {
		return errors.New("summary must not be empty")
	}
	if strings.TrimSpace(e.Category) == "" {
		return errors.New("category must not be empty")
	}
	if !validComplexity[e.Complexity] {
		return fmt.Errorf("complexity must be low, medium, or high, got %q", e.Complexity)
	}
	if e.EstimatedHoursMin < 0 || e.EstimatedHoursMax < 0 {
		return errors.New("estimated hours must not be negative")
	}
	if e.EstimatedHoursMin > e.EstimatedHoursMax {
		return errors.New("estimated_hours_min must not exceed estimated_hours_max")
	}
	if e.Risks == nil {
		return errors.New("risks must be an array")
	}
	if e.Assumptions == nil {
		return errors.New("assumptions must be an array")
	}
	return nil
}
