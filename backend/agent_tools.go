package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"aiwork/backend/internal/mcpclient"
)

// Day 17: the chat agent can call tools from the product's own GitHub
// Activity MCP server (cmd/mcp-github). The model sees them as ordinary
// OpenAI function tools; the host runs a small loop — model asks for a
// call, host forwards it over MCP, feeds the result back — until the model
// produces its usual JSON envelope.

// maxToolRounds bounds how many times one turn may go back to the model
// with tool results. The final round offers the tools with tool_choice
// "none", forcing an answer from what has been gathered so far.
const maxToolRounds = 4

// maxStoredEvents caps how many events a stored tool-call record keeps for
// the UI; the model itself always sees the full result.
const maxStoredEvents = 50

// ToolCallRecord is one MCP tool call made during a turn, stored on that
// turn's assistant message so the UI can show what the agent called, with
// which arguments, and what came back — and so it survives a reload.
type ToolCallRecord struct {
	Server     string          `json:"server"`
	ServerName string          `json:"server_name"`
	Tool       string          `json:"tool"`
	Arguments  json.RawMessage `json:"arguments"`
	OK         bool            `json:"ok"`
	Error      string          `json:"error,omitempty"`
	DurationMs int64           `json:"duration_ms"`
	Result     json.RawMessage `json:"result,omitempty"`
}

// SetActivityTools attaches the GitHub Activity MCP server connection whose
// tools every non-lab chat turn may call. nil disables tool use.
func (a *Agent) SetActivityTools(conn *mcpclient.Conn) {
	a.activityTools = conn
}

// availableTools lists the MCP server's tools in OpenAI function format.
// A server that can't be reached right now just means no tools for this
// turn — the chat still works, it only loses the activity lookup.
func (a *Agent) availableTools(ctx context.Context) []llmTool {
	if a.activityTools == nil {
		return nil
	}
	tools, err := a.activityTools.Tools(ctx)
	if err != nil {
		log.Printf("agent: activity MCP server unavailable, continuing without tools: %v", err)
		return nil
	}
	out := make([]llmTool, 0, len(tools))
	for _, t := range tools {
		var lt llmTool
		lt.Type = "function"
		lt.Function.Name = t.Name
		lt.Function.Description = t.Description
		lt.Function.Parameters = t.InputSchema
		out = append(out, lt)
	}
	return out
}

// toolUseSystemPrompt tells the model when to use the activity tools and
// anchors relative dates ("вчера", "на этой неделе"): the model has no
// clock of its own and otherwise guesses the date.
func toolUseSystemPrompt(now time.Time) string {
	zone, offset := now.Zone()
	return fmt.Sprintf(`Current local date and time: %s (%s), timezone %s (UTC%+03d:%02d). Weeks start on Monday.

You have read-only tools from the user's own GitHub Activity MCP server. Decide from the CURRENT user message alone:
- It asks about the user's own past work (what they did, committed, merged, reviewed or commented on in some period or repository) → call get_activity.
- It asks which repositories are tracked → call list_repos.
- Anything else — above all describing a task or asking to estimate one, or refining an existing estimate → call NO tools, even if activity was discussed earlier in this chat. Estimates do not use activity data yet.

When calling get_activity, compute since/until from the current local date above; for whole days pass plain YYYY-MM-DD dates (interpreted in the same local timezone, until is inclusive — for a single day pass the same date as both since and until). The period must not exceed 31 days. Make one call covering exactly the period asked about — don't widen it or split it into several calls. When the user names a repository, pass it in repos right away (the bare repo name is enough) — no need to call list_repos first or to fetch every repository.

Event kinds describe what happened in the period, not current state: pr_opened means the user created that PR during the period (it may well be merged or closed by now) — say "создала PR", never call such PRs "открытые"; pr_merged means it was merged during the period. The tool knows nothing about a PR's current state beyond these events.

Answer strictly from what the tool returned — never invent commits, PRs or repositories. Mention counts, group by repository when there are several, and link items with markdown links using the returned urls. Convert times to the local timezone. If the tool returned no events, say plainly that no activity was found for that period (and which repositories were checked). If it returned warnings, mention them briefly. If a tool call failed, say so honestly instead of guessing.

After using tools, still answer in the same JSON envelope as always, with "estimate": null.`,
		now.Format("2006-01-02 15:04"), russianWeekday(now.Weekday()), zone, offset/3600, abs(offset%3600)/60)
}

func russianWeekday(d time.Weekday) string {
	return [...]string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}[d]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// completeWithTools runs the model, executing any tool calls it asks for and
// feeding their results back, until it produces a final answer. usage sums
// every round (what the turn cost); contextTokens is the final round's
// total (how big the prompt actually got).
func (a *Agent) completeWithTools(ctx context.Context, chatID string, messages []chatMessage, maxTokens int, tools []llmTool) (completion *chatCompletionResponse, records []ToolCallRecord, usage *TokenUsage, contextTokens int, err error) {
	// The model was observed issuing the very same call twice in one
	// response; within a turn, an identical call (same tool, same raw
	// arguments) reuses the first result instead of hitting GitHub again.
	answered := map[string]string{}
	for round := 0; ; round++ {
		toolChoice := ""
		if round == maxToolRounds {
			toolChoice = "none"
		}
		emitTurnEvent(ctx, "round", map[string]int{"round": round + 1})
		completion, err = a.client.doChatCompletionWithTools(ctx, messages, 0.2, maxTokens, tools, toolChoice)
		if err != nil {
			return nil, records, usage, contextTokens, err
		}
		roundUsage := tokenUsageFrom(completion.Usage)
		usage = addTokenUsage(usage, roundUsage)
		if roundUsage != nil {
			contextTokens = roundUsage.TotalTokens
		}

		msg := completion.Choices[0].Message
		if len(msg.ToolCalls) == 0 || toolChoice == "none" {
			return completion, records, usage, contextTokens, nil
		}

		messages = append(messages, chatMessage{Role: "assistant", Content: msg.Content, ToolCalls: msg.ToolCalls})
		for _, call := range msg.ToolCalls {
			key := call.Function.Name + "\x00" + canonicalArgs(call.Function.Arguments)
			content, seen := answered[key]
			if seen {
				// Seen live: after an empty result the model re-issued the
				// same call round after round. Saying so explicitly stops it.
				content = "This exact call was already made in this turn and its result will not change — do not repeat it; answer from it. The result was:\n" + content
			} else {
				cfg := a.activityTools.Config()
				emitTurnEvent(ctx, "tool_call_started", toolCallStartedEvent{
					ID: call.ID, Server: cfg.ID, ServerName: cfg.Name, Tool: call.Function.Name,
					Arguments: argumentsForDisplay(call.Function.Arguments),
				})
				var record ToolCallRecord
				record, content = a.runToolCall(ctx, chatID, call)
				emitTurnEvent(ctx, "tool_call_finished", toolCallFinishedEvent{ID: call.ID, Record: record})
				records = append(records, record)
				answered[key] = content
			}
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: content})
		}
	}
}

// canonicalArgs re-encodes a JSON arguments object with sorted keys, so two
// calls differing only in key order (seen live: {"since",…,"repos"} then
// {"repos",…,"since"}) count as the same call. Invalid JSON is returned as is.
func canonicalArgs(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v) // map keys are marshaled in sorted order
	if err != nil {
		return raw
	}
	return string(out)
}

// argumentsForDisplay is the call's arguments as JSON for the progress
// event, or an empty object when the model sent something unparseable.
func argumentsForDisplay(raw string) json.RawMessage {
	if raw == "" || !json.Valid([]byte(raw)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(raw)
}

// runToolCall forwards one model-requested call to the MCP server. It never
// fails the turn: a bad call becomes a failed record plus an error text the
// model can explain to the user.
func (a *Agent) runToolCall(ctx context.Context, chatID string, call llmToolCall) (ToolCallRecord, string) {
	cfg := a.activityTools.Config()
	record := ToolCallRecord{Server: cfg.ID, ServerName: cfg.Name, Tool: call.Function.Name, Arguments: json.RawMessage("{}")}

	args := map[string]any{}
	if call.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			record.Error = "некорректные аргументы: " + err.Error()
			return record, "Tool call failed: arguments are not a valid JSON object: " + err.Error()
		}
		record.Arguments = json.RawMessage(call.Function.Arguments)
	}

	started := time.Now()
	result, err := a.activityTools.Call(ctx, call.Function.Name, args)
	record.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		log.Printf("agent: chat %s: tool %s failed: %v", chatID, call.Function.Name, err)
		record.Error = err.Error()
		return record, "Tool call failed: " + err.Error()
	}
	if result.IsError {
		log.Printf("agent: chat %s: tool %s returned an error: %s", chatID, call.Function.Name, truncateForLog(result.Text))
		record.Error = result.Text
		return record, "Tool returned an error: " + result.Text
	}

	record.OK = true
	record.Result = compactToolResult(result)
	log.Printf("agent: chat %s: tool %s(%s) ok in %dms", chatID, call.Function.Name, call.Function.Arguments, record.DurationMs)
	return record, result.Text
}

// compactToolResult is the structured result as stored for display, with an
// "events" array trimmed to maxStoredEvents (the count of dropped ones kept
// as "events_omitted") so one wide query can't bloat the chat file.
func compactToolResult(result *mcpclient.CallResult) json.RawMessage {
	raw := result.Structured
	if len(raw) == 0 {
		raw = json.RawMessage(result.Text)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	if events, ok := obj["events"].([]any); ok && len(events) > maxStoredEvents {
		obj["events"] = events[:maxStoredEvents]
		obj["events_omitted"] = len(events) - maxStoredEvents
	}
	compact, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return compact
}

// addTokenUsage sums two usages; either may be nil.
func addTokenUsage(sum, u *TokenUsage) *TokenUsage {
	if u == nil {
		return sum
	}
	if sum == nil {
		c := *u
		if u.CostUsd != nil {
			cost := *u.CostUsd
			c.CostUsd = &cost
		}
		return &c
	}
	sum.PromptTokens += u.PromptTokens
	sum.CompletionTokens += u.CompletionTokens
	sum.TotalTokens += u.TotalTokens
	if u.CostUsd != nil {
		if sum.CostUsd == nil {
			cost := *u.CostUsd
			sum.CostUsd = &cost
		} else {
			*sum.CostUsd += *u.CostUsd
		}
	}
	return sum
}
