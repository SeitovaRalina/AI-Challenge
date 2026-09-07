package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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

// expertRoles lists the panel personas in speaking order, matching the
// labels expertPanelSystemPrompt instructs the model to prefix each turn
// with, so parseExpertPanel can split the transcript by speaker.
var expertRoles = []string{"Аналитик", "Инженер", "Критик"}

// strategyRussianLabels gives the human-readable Russian name for each
// strategy, used to translate any raw enum identifier the judge model still
// slips into its rationale prose (see rewriteStrategyMentions).
var strategyRussianLabels = map[ReasoningStrategy]string{
	StrategyDirect:      "прямой ответ",
	StrategyStepByStep:  "пошаговое рассуждение",
	StrategyMetaPrompt:  "мета-промпт",
	StrategyExpertPanel: "экспертная группа",
}

// strategyMentionPattern matches any raw strategy identifier as a standalone
// word, so rewriteStrategyMentions can swap it for its Russian label without
// touching partial matches inside other words.
var strategyMentionPattern = regexp.MustCompile(`\b(direct|step_by_step|meta_prompt|expert_panel)\b`)

// rewriteStrategyMentions replaces any raw strategy identifier (e.g.
// "expert_panel") found in free-form rationale text with its Russian label,
// as a safety net in case the judge model ignores judgeSystemPrompt's
// instruction to always name strategies in Russian.
func rewriteStrategyMentions(text string) string {
	return strategyMentionPattern.ReplaceAllStringFunc(text, func(match string) string {
		if label, ok := strategyRussianLabels[ReasoningStrategy(match)]; ok {
			return label
		}
		return match
	})
}

const judgeSystemPrompt = `You are comparing four AI-generated software-development effort
estimates produced for the exact same task, using four different reasoning
strategies:
- "direct": no additional reasoning instructions.
- "step_by_step": the model was told to reason step by step before answering.
- "meta_prompt": the model first wrote its own prompt for the task, then that
  prompt was used to produce the estimate.
- "expert_panel": a simulated panel of an analyst, an engineer, and a critic
  discussed the task before a synthesized estimate.

Given the task and the four resulting estimates below, decide:
1. Whether the four estimates differ meaningfully (in hours, complexity, or
   key risks/assumptions) rather than just cosmetically.
2. Which single strategy produced the most accurate, best-reasoned estimate.
3. A short rationale in Russian (two to four sentences) explaining your
   choice and, if relevant, what differs between them.

Respond with ONLY a single JSON object, no markdown code fences, no
commentary before or after it, matching exactly this schema:

{
  "differs": true | false,
  "most_accurate": "direct" | "step_by_step" | "meta_prompt" | "expert_panel",
  "rationale": "..."
}

Write "rationale" in Russian. When referring to a strategy inside the
rationale text, always use its Russian name — «прямой ответ» for direct,
«пошаговое рассуждение» for step_by_step, «мета-промпт» for meta_prompt,
«экспертная группа» for expert_panel — and never the raw English identifier.
The "most_accurate" field itself must still be one of the exact raw enum
values above (never translate that field).`

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

// parseExpertPanel splits a panel discussion transcript into per-speaker
// turns, based on the "Имя: текст" line prefixes expertPanelSystemPrompt
// instructs the model to use. Returns nil if no known role prefix is found,
// so the caller can fall back to showing the raw transcript instead.
func parseExpertPanel(transcript string) []ExpertTurn {
	var turns []ExpertTurn
	var current *ExpertTurn

	for _, line := range strings.Split(transcript, "\n") {
		trimmed := strings.TrimSpace(line)

		matchedRole := ""
		for _, role := range expertRoles {
			if strings.HasPrefix(trimmed, role+":") {
				matchedRole = role
				break
			}
		}

		if matchedRole != "" {
			if current != nil {
				current.Text = strings.TrimSpace(current.Text)
				turns = append(turns, *current)
			}
			current = &ExpertTurn{
				Role: matchedRole,
				Text: strings.TrimSpace(strings.TrimPrefix(trimmed, matchedRole+":")),
			}
			continue
		}

		if current != nil && trimmed != "" {
			current.Text = strings.TrimSpace(current.Text + "\n" + trimmed)
		}
	}
	if current != nil {
		turns = append(turns, *current)
	}

	return turns
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

	step := &ReasoningStep{
		Estimate:    *estimate,
		LengthChars: len([]rune(cleaned)),
		LatencyMs:   latency,
	}
	if panel := parseExpertPanel(reasoning); len(panel) > 0 {
		step.Panel = panel
	} else {
		step.Reasoning = reasoning
	}
	return step, nil
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

// formatEstimateForJudge renders one strategy's estimate as plain text for
// the judge prompt, labeled by strategy so the judge can refer back to it.
func formatEstimateForJudge(label ReasoningStrategy, e EstimateResponse) string {
	return fmt.Sprintf(
		"%s:\n- category: %s\n- complexity: %s\n- estimated hours: %v-%v\n- summary: %s\n- risks: %s\n- assumptions: %s",
		label, e.Category, e.Complexity, e.EstimatedHoursMin, e.EstimatedHoursMax, e.Summary,
		strings.Join(e.Risks, "; "), strings.Join(e.Assumptions, "; "),
	)
}

// judgeReasoning asks the model to compare the four strategies' estimates
// for the same task and decide which one is most accurate.
func (c *LiteLLMClient) judgeReasoning(ctx context.Context, task string, direct, stepByStep, metaPrompt, expertPanel *ReasoningStep) (*ReasoningVerdict, error) {
	userMessage := task + "\n\n" + strings.Join([]string{
		formatEstimateForJudge(StrategyDirect, direct.Estimate),
		formatEstimateForJudge(StrategyStepByStep, stepByStep.Estimate),
		formatEstimateForJudge(StrategyMetaPrompt, metaPrompt.Estimate),
		formatEstimateForJudge(StrategyExpertPanel, expertPanel.Estimate),
	}, "\n\n")

	content, err := c.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: judgeSystemPrompt},
		{Role: "user", Content: userMessage},
	}, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}

	cleaned := stripCodeFences(content)
	var verdict ReasoningVerdict
	if err := json.Unmarshal([]byte(cleaned), &verdict); err != nil {
		return nil, fmt.Errorf("%w: judge did not return valid JSON: %v", ErrInvalidOutput, err)
	}
	if err := verdict.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	verdict.Rationale = rewriteStrategyMentions(verdict.Rationale)

	return &verdict, nil
}

// CompareReasoning solves the same task with four reasoning strategies in
// parallel — direct, step-by-step, meta-prompting, and an expert panel —
// then asks the model to judge which one is most accurate, so the four
// results and that verdict can be shown together.
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

	verdict, err := c.judgeReasoning(ctx, task, direct, stepByStep, metaPrompt, expertPanel)
	if err != nil {
		return nil, err
	}

	return &ReasoningResponse{
		Task:        task,
		Direct:      *direct,
		StepByStep:  *stepByStep,
		MetaPrompt:  *metaPrompt,
		ExpertPanel: *expertPanel,
		Verdict:     *verdict,
	}, nil
}
