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
// OpenAI function tools; when it asks for a call, the host forwards it over
// MCP and feeds the result back — see completeWithTools for how a turn is
// split into a tool-routing step and the answering call.

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

// Tool use runs as two separate model calls per turn, not one call that is
// offered the tools alongside the usual JSON envelope. Seen live with the
// gateway's model: when tools were offered to the main call, it repeatedly
// pushed its answer into the tool-call channel — reasoning "a refinement of
// the estimate, no tools needed" and then calling get_activity anyway, or
// emitting its whole JSON envelope as a call to a made-up "invoke" tool.
// Prompt rules could not stop it. So:
//
//  1. Routing: a small call with only the recent conversation and the tools.
//     Its whole job is to call tools (possibly over several rounds) or to
//     answer "NONE" — there's no big JSON answer that could slip into a
//     tool call.
//  2. Answer: the ordinary main call. Without tool results it gets no tools
//     at all — exactly the pre-day-17 behavior, so estimates are untouched.
//     With results, they're appended to the conversation and the tools are
//     described with tool_choice "none", so it can only answer.

// maxRoutingRounds bounds how many times routing may go back for more tool
// calls after seeing results.
const maxRoutingRounds = 3

// routingHistoryMessages is how much recent conversation routing sees —
// enough to resolve follow-ups like "а вчера?", far less than the full
// prompt.
const routingHistoryMessages = 6

// routingMaxTokens caps a routing call; the gateway's model reasons before
// answering, so this leaves room for that plus a tool call.
const routingMaxTokens = 4096

func localTimeLine(now time.Time) string {
	zone, offset := now.Zone()
	return fmt.Sprintf("Current local date and time: %s (%s), timezone %s (UTC%+03d:%02d). Weeks start on Monday.",
		now.Format("2006-01-02 15:04"), russianWeekday(now.Weekday()), zone, offset/3600, abs(offset%3600)/60)
}

// routingSystemPrompt decides whether the latest message needs a tool. It
// also anchors relative dates ("вчера", "на этой неделе"): the model has no
// clock of its own and otherwise guesses the date.
func routingSystemPrompt(now time.Time) string {
	return localTimeLine(now) + `

You are the tool-routing step of a work assistant. You never answer the user yourself — another step does. Your only job: decide whether the LATEST user message needs data from the user's own GitHub Activity MCP server, and if so, fetch it.

- It asks about the user's own past work (what they did, committed, merged, reviewed or commented on in some period or repository) → call get_activity.
- It asks which repositories are tracked → call list_repos.
- Anything else — describing a task, asking to estimate one, refining an estimate, small talk → reply with exactly: NONE

Earlier messages are there only to resolve follow-ups (e.g. "а вчера?" after an activity question); they never make a task or estimate message need a tool.

When calling get_activity, compute since/until from the current local date above; for whole days pass plain YYYY-MM-DD dates (same local timezone; until is inclusive — for a single day pass the same date as both). At most 31 days. Make one call covering exactly the period asked about. When the user names a repository, pass it in repos right away (the bare repo name is enough).

After you receive tool results: if they cover the question, reply with exactly: NONE. Call again only if the question genuinely needs other data (e.g. a second period to compare).`
}

// toolResultsSystemPrompt tells the answering call how to use the results
// routing fetched.
func toolResultsSystemPrompt(now time.Time) string {
	return localTimeLine(now) + `

For the user's latest message, data was fetched from the user's own GitHub Activity MCP server; the tool calls and their results follow that message.

Event kinds describe what happened in the period, not current state: pr_opened means the user created that PR during the period (it may well be merged or closed by now) — say "создала PR", never call such PRs "открытые"; pr_merged means it was merged during the period. The tool knows nothing about a PR's current state beyond these events.

Answer strictly from what the tools returned — never invent commits, PRs or repositories. Mention counts, group by repository when there are several, and link items with markdown links using the returned urls. Times are already in the local timezone. If no events were found, say plainly that no activity was found for that period (and which repositories were checked). If there were warnings, mention them briefly. If a tool call failed, say so honestly instead of guessing.

Answer in the same JSON envelope as always, with "estimate": null.`
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

// completeWithTools produces the turn's final completion: routing (tool
// calls, if any) first, then the answering call over messages — the fully
// built prompt, ending with the user's message. usage sums every call (what
// the turn cost); contextTokens is the answering call's total (how big the
// prompt actually got).
func (a *Agent) completeWithTools(ctx context.Context, chatID string, messages []chatMessage, maxTokens int, tools []llmTool) (completion *chatCompletionResponse, records []ToolCallRecord, usage *TokenUsage, contextTokens int, err error) {
	var transcript []chatMessage
	if len(tools) > 0 {
		transcript, records, usage = a.routeTools(ctx, chatID, messages, tools)
	}

	answerMessages := messages
	var answerTools []llmTool
	toolChoice := ""
	if len(transcript) > 0 {
		last := len(messages) - 1
		answerMessages = make([]chatMessage, 0, len(messages)+1+len(transcript))
		answerMessages = append(answerMessages, messages[:last]...)
		answerMessages = append(answerMessages, chatMessage{Role: "system", Content: toolResultsSystemPrompt(time.Now())})
		answerMessages = append(answerMessages, messages[last])
		answerMessages = append(answerMessages, transcript...)
		// The history now holds tool calls, which the API only accepts with
		// the tools described — "none" keeps this call answer-only.
		answerTools, toolChoice = tools, "none"
	}

	emitTurnEvent(ctx, "answering", struct{}{})
	completion, err = a.client.doChatCompletionWithTools(ctx, answerMessages, 0.2, maxTokens, answerTools, toolChoice)
	if err != nil {
		return nil, records, usage, contextTokens, err
	}
	answerUsage := tokenUsageFrom(completion.Usage)
	usage = addTokenUsage(usage, answerUsage)
	if answerUsage != nil {
		contextTokens = answerUsage.TotalTokens
	}
	return completion, records, usage, contextTokens, nil
}

// routeTools runs the routing step and executes the tool calls it asks for.
// It returns the assistant tool-call / tool-result messages to append after
// the user's message for the answering call (nil when no tool was needed).
// Routing failures never fail the turn — the answer simply goes without
// tools.
func (a *Agent) routeTools(ctx context.Context, chatID string, messages []chatMessage, tools []llmTool) (transcript []chatMessage, records []ToolCallRecord, usage *TokenUsage) {
	routing := []chatMessage{{Role: "system", Content: routingSystemPrompt(time.Now())}}
	routing = append(routing, recentConversation(messages, routingHistoryMessages)...)

	// The model was observed issuing the very same call twice; within a
	// turn, an identical call (same tool, same arguments in any key order)
	// reuses the first result instead of hitting GitHub again.
	answered := map[string]string{}
	for round := 0; round < maxRoutingRounds; round++ {
		emitTurnEvent(ctx, "routing", map[string]int{"round": round + 1})
		completion, err := a.client.doChatCompletionWithTools(ctx, routing, 0, routingMaxTokens, tools, "")
		if err != nil {
			log.Printf("agent: chat %s: tool routing failed, answering without tools: %v", chatID, err)
			return transcript, records, usage
		}
		usage = addTokenUsage(usage, tokenUsageFrom(completion.Usage))
		msg := completion.Choices[0].Message
		if len(msg.ToolCalls) == 0 {
			return transcript, records, usage
		}

		step := []chatMessage{{Role: "assistant", Content: msg.Content, ToolCalls: msg.ToolCalls}}
		for _, call := range msg.ToolCalls {
			key := call.Function.Name + "\x00" + canonicalArgs(call.Function.Arguments)
			content, seen := answered[key]
			if seen {
				content = "This exact call was already made in this turn and its result will not change — do not repeat it. The result was:\n" + content
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
			step = append(step, chatMessage{Role: "tool", ToolCallID: call.ID, Content: content})
		}
		routing = append(routing, step...)
		transcript = append(transcript, step...)
	}
	return transcript, records, usage
}

// recentConversation is the last n user/assistant messages of messages
// (system messages dropped), always ending with the user's latest message.
func recentConversation(messages []chatMessage, n int) []chatMessage {
	var out []chatMessage
	for i := len(messages) - 1; i >= 0 && len(out) < n; i-- {
		if m := messages[i]; m.Role == "user" || m.Role == "assistant" {
			out = append(out, chatMessage{Role: m.Role, Content: m.Content})
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
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
