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

// ExpertTurn is one persona's contribution to the expert-panel strategy's
// internal discussion, parsed out of the model's raw reasoning text.
type ExpertTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// ReasoningStep is one reasoning strategy's result for the day-3 comparison.
// Reasoning holds free-form reasoning text (step-by-step, or expert-panel
// when its discussion couldn't be split into Panel turns); Panel holds the
// expert-panel strategy's discussion structured by speaker; GeneratedPrompt
// holds the model-authored prompt for the meta-prompting strategy. All three
// are empty for the direct strategy, which has none of them.
type ReasoningStep struct {
	Reasoning       string           `json:"reasoning,omitempty"`
	Panel           []ExpertTurn     `json:"panel,omitempty"`
	GeneratedPrompt string           `json:"generated_prompt,omitempty"`
	Estimate        EstimateResponse `json:"estimate"`
	LengthChars     int              `json:"length_chars"`
	LatencyMs       int64            `json:"latency_ms"`
}

// ReasoningStrategy identifies one of the four day-3 reasoning strategies.
type ReasoningStrategy string

const (
	StrategyDirect      ReasoningStrategy = "direct"
	StrategyStepByStep  ReasoningStrategy = "step_by_step"
	StrategyMetaPrompt  ReasoningStrategy = "meta_prompt"
	StrategyExpertPanel ReasoningStrategy = "expert_panel"
)

var validReasoningStrategy = map[ReasoningStrategy]bool{
	StrategyDirect:      true,
	StrategyStepByStep:  true,
	StrategyMetaPrompt:  true,
	StrategyExpertPanel: true,
}

// ReasoningVerdict is an LLM judge's comparison of the four strategies'
// results for the same task.
type ReasoningVerdict struct {
	Differs      bool              `json:"differs"`
	MostAccurate ReasoningStrategy `json:"most_accurate"`
	Rationale    string            `json:"rationale"`
}

// Validate rejects a judge response that doesn't name one of the four known
// strategies or gives no rationale.
func (v ReasoningVerdict) Validate() error {
	if !validReasoningStrategy[v.MostAccurate] {
		return fmt.Errorf("most_accurate must be one of direct, step_by_step, meta_prompt, or expert_panel, got %q", v.MostAccurate)
	}
	if strings.TrimSpace(v.Rationale) == "" {
		return errors.New("rationale must not be empty")
	}
	return nil
}

// ReasoningResponse holds the same task solved via four reasoning
// strategies, plus an LLM judge's verdict on which is most accurate.
type ReasoningResponse struct {
	Task        string           `json:"task"`
	Direct      ReasoningStep    `json:"direct"`
	StepByStep  ReasoningStep    `json:"step_by_step"`
	MetaPrompt  ReasoningStep    `json:"meta_prompt"`
	ExpertPanel ReasoningStep    `json:"expert_panel"`
	Verdict     ReasoningVerdict `json:"verdict"`
}

// TemperatureRequest is the payload accepted by POST /api/temperature.
type TemperatureRequest struct {
	Task string `json:"task"`
}

// temperatureValues are the three sampling temperatures compared for the
// day-4 challenge, in low-to-high order.
var temperatureValues = []float64{0, 0.7, 1.2}

// TemperatureResult is one temperature's raw, unconstrained response to the
// same task, alongside its sampling temperature.
type TemperatureResult struct {
	Temperature float64 `json:"temperature"`
	Text        string  `json:"text"`
	LengthChars int     `json:"length_chars"`
	LatencyMs   int64   `json:"latency_ms"`
}

// TemperatureAnalysis is an LLM judge's breakdown of one temperature's
// response along the three axes the day-4 challenge asks to compare, plus
// the kinds of tasks that temperature suits.
type TemperatureAnalysis struct {
	Temperature float64  `json:"temperature"`
	Accuracy    string   `json:"accuracy"`
	Creativity  string   `json:"creativity"`
	Diversity   string   `json:"diversity"`
	BestFor     []string `json:"best_for"`
}

// Validate rejects an analysis entry that doesn't name one of the three
// compared temperatures or is missing any of its required prose fields.
func (a TemperatureAnalysis) Validate() error {
	valid := false
	for _, t := range temperatureValues {
		if a.Temperature == t {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("temperature must be one of %v, got %v", temperatureValues, a.Temperature)
	}
	if strings.TrimSpace(a.Accuracy) == "" {
		return errors.New("accuracy must not be empty")
	}
	if strings.TrimSpace(a.Creativity) == "" {
		return errors.New("creativity must not be empty")
	}
	if strings.TrimSpace(a.Diversity) == "" {
		return errors.New("diversity must not be empty")
	}
	if len(a.BestFor) == 0 {
		return errors.New("best_for must not be empty")
	}
	return nil
}

// TemperatureVerdict is the LLM judge's full comparison of the three
// temperature responses: a per-temperature breakdown plus an overall
// takeaway.
type TemperatureVerdict struct {
	Analysis []TemperatureAnalysis `json:"analysis"`
	Summary  string                `json:"summary"`
}

// Validate rejects a verdict that doesn't cover exactly the three compared
// temperatures or gives no overall summary.
func (v TemperatureVerdict) Validate() error {
	if len(v.Analysis) != len(temperatureValues) {
		return fmt.Errorf("analysis must have %d entries, got %d", len(temperatureValues), len(v.Analysis))
	}
	for _, a := range v.Analysis {
		if err := a.Validate(); err != nil {
			return err
		}
	}
	if strings.TrimSpace(v.Summary) == "" {
		return errors.New("summary must not be empty")
	}
	return nil
}

// TemperatureResponse holds the same task solved at three sampling
// temperatures, plus an LLM judge's comparison of the three.
type TemperatureResponse struct {
	Task    string              `json:"task"`
	Results []TemperatureResult `json:"results"`
	Verdict TemperatureVerdict  `json:"verdict"`
}

// ModelRequest is the payload accepted by POST /api/models.
type ModelRequest struct {
	Task string `json:"task"`
}

// ModelTier identifies one of the three day-5 model-capability tiers. The
// three are ordered by each model's own documented count of active MoE
// parameters, not by vendor branding — this LiteLLM gateway's "claude-*" and
// "gpt-*" model IDs are cosmetic aliases that get routed to an arbitrary
// (if stable per-alias) backend, not the real Anthropic/OpenAI models, so
// they can't be trusted to reflect a genuine weak/medium/strong ordering.
type ModelTier string

const (
	ModelTierWeak   ModelTier = "weak"
	ModelTierMedium ModelTier = "medium"
	ModelTierStrong ModelTier = "strong"
)

var validModelTier = map[ModelTier]bool{
	ModelTierWeak:   true,
	ModelTierMedium: true,
	ModelTierStrong: true,
}

// ModelInfo describes one of the three compared models: its real LiteLLM
// model ID (directly named, not a branded alias), a display name, a Russian
// description noting its documented active-parameter count, and a link to
// its official model card.
type ModelInfo struct {
	Tier        ModelTier
	ModelID     string
	Name        string
	Description string
	DocsURL     string
}

// comparedModels are the three models day-5 compares, weak to strong, picked
// because each ID names its real underlying model directly (verified: a
// repeated request for the same ID always routes to the same backend) with a
// documented, monotonically increasing active-parameter count: GLM-4.7-Flash
// (3B active / 31.2B total), DeepSeek-V4-Flash (13B active / 284B total),
// DeepSeek-V4-Pro (49B active / 1.6T total).
var comparedModels = []ModelInfo{
	{
		Tier:        ModelTierWeak,
		ModelID:     "glm-4.7-flash",
		Name:        "GLM-4.7-Flash",
		Description: "Слабая модель — 3B активных параметров (31.2B всего, MoE).",
		DocsURL:     "https://huggingface.co/zai-org/GLM-4.7-Flash",
	},
	{
		Tier:        ModelTierMedium,
		ModelID:     "deepseek-v4-flash-0731",
		Name:        "DeepSeek-V4-Flash",
		Description: "Средняя модель — 13B активных параметров (284B всего, MoE).",
		DocsURL:     "https://huggingface.co/deepseek-ai/DeepSeek-V4-Flash-0731",
	},
	{
		Tier:        ModelTierStrong,
		ModelID:     "deepseek-v4-pro",
		Name:        "DeepSeek-V4-Pro",
		Description: "Сильная модель — 49B активных параметров (1.6T всего, MoE).",
		DocsURL:     "https://huggingface.co/deepseek-ai/DeepSeek-V4-Pro",
	},
}

// ModelResult is one model's measured response to the same task: latency,
// token usage, and cost are read directly from the LiteLLM gateway's own
// response, never estimated.
type ModelResult struct {
	Tier             ModelTier `json:"tier"`
	ModelID          string    `json:"model_id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	DocsURL          string    `json:"docs_url"`
	Text             string    `json:"text"`
	LatencyMs        int64     `json:"latency_ms"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	CostUsd          *float64  `json:"cost_usd,omitempty"`
}

// ModelQualityAssessment is the LLM judge's take on one model's answer
// quality only. Speed, token usage, and cost are measured directly (see
// ModelResult) rather than judged.
type ModelQualityAssessment struct {
	Tier    ModelTier `json:"tier"`
	Quality string    `json:"quality"`
}

// Validate rejects a quality assessment that doesn't name one of the three
// known tiers or gives no assessment text.
func (a ModelQualityAssessment) Validate() error {
	if !validModelTier[a.Tier] {
		return fmt.Errorf("tier must be one of weak, medium, or strong, got %q", a.Tier)
	}
	if strings.TrimSpace(a.Quality) == "" {
		return errors.New("quality must not be empty")
	}
	return nil
}

// ModelVerdict is the LLM judge's quality comparison across the three
// models, plus an overall takeaway with a task-specific recommendation.
type ModelVerdict struct {
	Quality []ModelQualityAssessment `json:"quality"`
	Summary string                   `json:"summary"`
}

// Validate rejects a verdict that doesn't cover exactly the three compared
// tiers or gives no overall summary.
func (v ModelVerdict) Validate() error {
	if len(v.Quality) != len(comparedModels) {
		return fmt.Errorf("quality must have %d entries, got %d", len(comparedModels), len(v.Quality))
	}
	for _, q := range v.Quality {
		if err := q.Validate(); err != nil {
			return err
		}
	}
	if strings.TrimSpace(v.Summary) == "" {
		return errors.New("summary must not be empty")
	}
	return nil
}

// ModelComparisonResponse holds the same task solved by three models of
// increasing capability tier, plus an LLM judge's quality comparison.
type ModelComparisonResponse struct {
	Task    string        `json:"task"`
	Results []ModelResult `json:"results"`
	Verdict ModelVerdict  `json:"verdict"`
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
