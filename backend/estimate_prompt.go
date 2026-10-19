package main

import "fmt"

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
