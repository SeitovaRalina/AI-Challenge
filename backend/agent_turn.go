package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// agentSystemPrompt wraps the day-1 estimate schema (systemPrompt) with
// conversational behavior: the first user message is treated as a task
// description and produces an initial estimate, and every later message is
// answered using the full conversation history, only re-issuing (and
// overwriting) the estimate when the user's message actually changes it.
const agentSystemPrompt = systemPrompt + `

You are embedded in a chat interface, not a one-shot form. Always answer in
this envelope, as a single JSON object with no markdown fences and no text
outside it:

{
  "reply": "the message shown to the user in the chat, in Russian, conversational, may reference the estimate but does not need to repeat every field",
  "estimate": <the EstimateResponse object described above> or null
}

This applies even to a plain conversational answer that changes nothing (a
clarifying question, summing existing subtask hours, small talk) — NEVER
respond with bare prose outside this envelope, even then; put that prose in
"reply" and set "estimate" to null.

Set "estimate" to a full, updated EstimateResponse object only when this
message is the task description itself, or when the user's message changes
the estimate (new details, constraints, or an explicit request to redo it).
Otherwise set "estimate" to null and use "reply" for ordinary conversation
(answering questions, discussing the task, clarifying assumptions) — do not
invent a new estimate on every turn just because one exists already.

Whenever you do set "estimate", additionally include a "subtasks" field: an
array of {"name", "description", "estimated_hours_min", "estimated_hours_max"}
breaking the task into its logical pieces. Only break a task down when it is
actually large enough to benefit from that (roughly, when your own total
estimate is beyond a day or two of work, or the task clearly bundles several
distinct pieces of work) — for a small, atomic task, "subtasks" must be an
empty array rather than artificially split into filler pieces. The top-level
"estimated_hours_min"/"estimated_hours_max" MUST equal the sum of all
subtasks' own min/max hours — never a separately guessed number. Every "reply"
you write afterward, including when "estimate" is null, MUST stay consistent
with the current subtasks: if the user asks how long a specific subtask will
take, answer from its exact estimated_hours_min/estimated_hours_max instead
of guessing.`

// agentTurn is the envelope the agent asks the model for on every turn.
type agentTurn struct {
	Reply    string            `json:"reply"`
	Estimate *EstimateResponse `json:"estimate"`
}

// PostMessage appends the user's message to chatID's active branch (or its
// plain history, for every strategy but branching), asks the LLM for a reply
// using that strategy's own view of the conversation so far, and stores the
// assistant's reply back into that same place.
func (a *Agent) PostMessage(ctx context.Context, chatID, userMessage string) (*AgentReply, error) {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok {
		a.mu.Unlock()
		return nil, ErrChatNotFound
	}
	// Snapshot everything PostMessage needs under the lock, then release it
	// for the (slow) LLM call — a chat is only ever driven by one user (or,
	// for a lab's fan-out, one background goroutine per sibling chat), so
	// this is safe.
	history := append([]AgentMessage(nil), chat.activeMessages()...)
	currentEstimate := chat.Estimate
	lastContextTokens := chat.LastContextTokens
	strategy := chat.ContextStrategy
	facts := chat.Facts
	summary := chat.Summary
	summarizedThrough := chat.SummarizedThrough
	a.mu.Unlock()

	log.Printf("agent: chat %s: turn %d, strategy %s, message length %d", chatID, len(history)/2+1, strategy, len(userMessage))

	userSentAt := time.Now()

	// Pre-call overflow guard: the previous turn's prompt_tokens PLUS its own
	// completion_tokens is what's actually sitting in context right now — the
	// reply the model just wrote is appended to the conversation and will
	// itself be sent back as part of the prompt on the next call, so it
	// already occupies context space even though it was never sent as a
	// prompt yet. This turn's history is at most a couple of messages larger
	// than that — close enough to treat as this turn's starting budget.
	// Rather than let the model write an unbounded reply and only check the
	// total afterward, the real call's max_tokens is capped to whatever room
	// is left, so the upstream API itself truncates (finish_reason "length")
	// if the answer would need more room than remains. Below
	// minCompletionBudget there isn't enough room left for a coherent reply,
	// so that case still skips the call entirely.
	const minCompletionBudget = 64
	remainingBudget := 0
	if a.contextTokenLimit > 0 {
		remainingBudget = a.contextTokenLimit - lastContextTokens
	}
	if a.contextTokenLimit > 0 && remainingBudget < minCompletionBudget {
		log.Printf("agent: chat %s: context limit reached (%d/%d tokens, %d left), skipping LLM call",
			chatID, lastContextTokens, a.contextTokenLimit, remainingBudget)
		reply := contextFullReplyText(lastContextTokens, a.contextTokenLimit)
		return a.finishGracefulTurn(ctx, chat, userMessage, reply, userSentAt, nil)
	}

	messages := make([]chatMessage, 0, len(history)+3)
	messages = append(messages, chatMessage{Role: "system", Content: agentSystemPrompt})
	// Re-stating the exact current estimate (subtasks included) as its own
	// system message means a question like "how long will X take" is answered
	// from these precise numbers, not from however the prior reply phrased it.
	if currentEstimate != nil {
		if encoded, err := json.Marshal(currentEstimate); err == nil {
			messages = append(messages, chatMessage{
				Role:    "system",
				Content: "Текущая актуальная оценка задачи (JSON, используй точные числа при ответах про сроки): " + string(encoded),
			})
		}
	}
	extra, raw := buildContextMessages(strategy, a.historyKeepLastN, history, facts, summary, summarizedThrough)
	messages = append(messages, extra...)
	for _, m := range raw {
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, chatMessage{Role: "user", Content: userMessage})

	maxTokens := 0
	if a.contextTokenLimit > 0 {
		maxTokens = remainingBudget
	}

	callStart := time.Now()
	completion, err := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, maxTokens, nil)
	if err != nil {
		// A real upstream context-length rejection is handled the same
		// gracefully-in-chat way as the pre-call guard above, instead of
		// surfacing as a 502 to the user.
		if errors.Is(err, ErrContextOverflow) {
			log.Printf("agent: chat %s: model rejected the request as over its context length: %v", chatID, err)
			reply := contextFullReplyText(lastContextTokens, a.contextTokenLimit)
			return a.finishGracefulTurn(ctx, chat, userMessage, reply, userSentAt, nil)
		}
		log.Printf("agent: chat %s: LLM call failed after %s: %v", chatID, time.Since(callStart).Round(time.Millisecond), err)
		return nil, err
	}
	finishReason := completion.Choices[0].FinishReason
	log.Printf("agent: chat %s: LLM call took %s (finish_reason=%s)", chatID, time.Since(callStart).Round(time.Millisecond), finishReason)
	if finishReason == "length" {
		log.Printf("agent: chat %s: response truncated by the %d-token context budget — real API enforced the simulated context window", chatID, maxTokens)
	}

	turn, err := parseAgentTurn(completion.Choices[0].Message.Content)
	if err != nil {
		// A reasoning model can spend its entire truncated budget on hidden
		// reasoning and never emit any visible content at all — worse than a
		// mid-sentence cutoff, there's nothing to fall back to as prose
		// either. This is NOT the dialog running out of room — it's this one
		// reply that needed more than the remaining per-turn budget, so it
		// gets its own honest message instead of the "context limit reached"
		// one.
		if finishReason == "length" {
			log.Printf("agent: chat %s: truncated response was unusable (finish_reason=length, %d-token budget): %v", chatID, maxTokens, err)
			// The call genuinely happened and cost real tokens — usage is
			// still populated even though the content came out unusable, so
			// the chat's running totals must reflect it.
			usage := tokenUsageFrom(completion.Usage)
			reply := truncatedReplyText(maxTokens)
			return a.finishGracefulTurn(ctx, chat, userMessage, reply, userSentAt, usage)
		}
		log.Printf("agent: chat %s: failed to parse LLM turn: %v; raw response: %s", chatID, err, truncateForLog(completion.Choices[0].Message.Content))
		return nil, err
	}
	assistantSentAt := time.Now()

	usage := tokenUsageFrom(completion.Usage)
	if usage != nil {
		log.Printf("agent: chat %s: usage prompt=%d completion=%d total=%d",
			chatID, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}

	a.mu.Lock()

	chat.appendActiveMessages(
		AgentMessage{Role: "user", Content: userMessage, CreatedAt: userSentAt, Usage: usage},
		AgentMessage{Role: "assistant", Content: turn.Reply, CreatedAt: assistantSentAt, Usage: usage},
	)
	if turn.Estimate != nil {
		chat.Estimate = turn.Estimate
		log.Printf("agent: chat %s: estimate updated, %.1f-%.1fh, %d subtask(s)",
			chatID, turn.Estimate.EstimatedHoursMin, turn.Estimate.EstimatedHoursMax, len(turn.Estimate.Subtasks))
	}
	if chat.Title == "Новый чат" {
		chat.Title = chatTitleFrom(userMessage)
	}
	if usage != nil {
		chat.LastContextTokens = usage.TotalTokens
		chat.addUsage(usage)
	}

	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}

	agentReply := a.buildAgentReplyLocked(chat, turn.Reply, usage, userSentAt, assistantSentAt)
	a.mu.Unlock()

	// Runs its own (possibly slow) LLM calls outside the lock just released,
	// so a strategy side-call for this chat never blocks any other chat.
	// agentReply's strategy fields were captured BEFORE these calls, so if
	// one actually changes something, refresh them afterward — otherwise
	// this turn's own response would understate what just happened to its
	// own chat.
	if strategy == StrategyStickyFacts {
		if updated := a.updateFactsAfterTurn(ctx, chatID, userMessage, turn.Reply); updated != nil {
			a.mu.Lock()
			if c, ok := a.chats[chatID]; ok {
				agentReply.Facts = c.Facts
				agentReply.CumulativeTotalTokens = c.CumulativeTotalTokens
				agentReply.CumulativeCostUsd = c.CumulativeCostUsd
			}
			a.mu.Unlock()
		}
	}
	if event := a.compressHistoryIfDue(ctx, chatID); event != nil {
		agentReply.NewCompressionEvent = event
		a.mu.Lock()
		if c, ok := a.chats[chatID]; ok {
			agentReply.SummarizedMessageCount = c.SummarizedThrough
			agentReply.RawMessageCount = len(c.Messages) - c.SummarizedThrough
			agentReply.CumulativeTotalTokens = c.CumulativeTotalTokens
			agentReply.CumulativeCostUsd = c.CumulativeCostUsd
		}
		a.mu.Unlock()
	}

	return agentReply, nil
}

// buildAgentReplyLocked assembles an AgentReply from chat's current state.
// Callers must hold a.mu. Shared by PostMessage's success path and
// finishGracefulTurn so both return the exact same shape.
func (a *Agent) buildAgentReplyLocked(chat *Chat, reply string, usage *TokenUsage, userSentAt, assistantSentAt time.Time) *AgentReply {
	isCoordinator := false
	if chat.LabID != "" {
		if lab, ok := a.labs[chat.LabID]; ok {
			isCoordinator = lab.CoordinatorChatID == chat.ID
		}
	}
	return &AgentReply{
		Reply:                     reply,
		Estimate:                  chat.Estimate,
		Title:                     chat.Title,
		Usage:                     usage,
		UserMessageCreatedAt:      userSentAt,
		AssistantMessageCreatedAt: assistantSentAt,
		LastContextTokens:         chat.LastContextTokens,
		CumulativeTotalTokens:     chat.CumulativeTotalTokens,
		CumulativeCostUsd:         chat.CumulativeCostUsd,
		ContextTokenLimit:         a.contextTokenLimit,
		ContextStrategy:           chat.ContextStrategy,
		HistoryKeepLastN:          a.historyKeepLastN,
		SummarizedMessageCount:    chat.SummarizedThrough,
		RawMessageCount:           len(chat.Messages) - chat.SummarizedThrough,
		Facts:                     chat.Facts,
		Branches:                  branchSummaries(chat),
		ActiveBranchID:            chat.ActiveBranchID,
		LabID:                     chat.LabID,
		IsLabCoordinator:          isCoordinator,
	}
}

// contextFullReplyText is shown when the dialog's history genuinely leaves
// no usable room left — the two cases where the LLM was never even called
// (the pre-call guard) or was rejected outright by the upstream API.
func contextFullReplyText(tokens, limit int) string {
	return fmt.Sprintf(
		"⚠️ Достигнут лимит контекста диалога: %d / %d токенов. "+
			"Продолжить этот диалог нельзя — начните новый чат.",
		tokens, limit,
	)
}

// truncatedReplyText is shown when the history itself still had room, but
// this one turn's reply needed more than what remained and got cut off by
// the model provider (finish_reason "length") into something unusable.
func truncatedReplyText(budget int) string {
	return fmt.Sprintf(
		"⚠️ Ответ модели получился длиннее допустимого бюджета для одного "+
			"хода (%d токенов) и был обрезан. Попробуйте задать более "+
			"короткий или простой вопрос, либо начните новый чат.",
		budget,
	)
}

// finishGracefulTurn appends the user's message and a synthetic reply to
// chat, persists it, and returns the same AgentReply shape a normal turn
// would — so the frontend needs no special-casing for any of the paths that
// call it. reply is built by the caller (contextFullReplyText or
// truncatedReplyText) so each failure mode gets accurate wording.
//
// usage is nil when the LLM was never actually called (the pre-call guard,
// or an outright upstream rejection). When a call DID happen but its content
// came out unusable, pass its real usage: the request genuinely happened and
// cost real tokens, so the chat's running totals must reflect it.
func (a *Agent) finishGracefulTurn(ctx context.Context, chat *Chat, userMessage, reply string, userSentAt time.Time, usage *TokenUsage) (*AgentReply, error) {
	assistantSentAt := time.Now()

	a.mu.Lock()

	chat.appendActiveMessages(
		AgentMessage{Role: "user", Content: userMessage, CreatedAt: userSentAt, Usage: usage},
		AgentMessage{Role: "assistant", Content: reply, CreatedAt: assistantSentAt, Usage: usage},
	)
	if chat.Title == "Новый чат" {
		chat.Title = chatTitleFrom(userMessage)
	}
	if usage != nil {
		chat.LastContextTokens = usage.TotalTokens
		chat.addUsage(usage)
	}
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}

	chatID := chat.ID
	agentReply := a.buildAgentReplyLocked(chat, reply, usage, userSentAt, assistantSentAt)
	a.mu.Unlock()

	// A graceful turn (context full, or a truncated reply) never touches
	// facts — there's no real assistant content to extract them from — but
	// history has still grown, so a rolling_summary chat may still be due
	// for a fold.
	if event := a.compressHistoryIfDue(ctx, chatID); event != nil {
		agentReply.NewCompressionEvent = event
		a.mu.Lock()
		if c, ok := a.chats[chatID]; ok {
			agentReply.SummarizedMessageCount = c.SummarizedThrough
			agentReply.RawMessageCount = len(c.Messages) - c.SummarizedThrough
			agentReply.CumulativeTotalTokens = c.CumulativeTotalTokens
			agentReply.CumulativeCostUsd = c.CumulativeCostUsd
		}
		a.mu.Unlock()
	}

	return agentReply, nil
}

// parseAgentTurn strips optional code fences and unmarshals the model's
// {"reply", "estimate"} envelope, validating the estimate against the same
// schema the day-1 estimate endpoint enforces whenever one is present.
func parseAgentTurn(raw string) (*agentTurn, error) {
	cleaned := stripCodeFences(raw)

	var turn agentTurn
	if err := json.Unmarshal([]byte(cleaned), &turn); err != nil {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: model did not return valid JSON: %v", ErrInvalidOutput, err)
		}
		// cleaned starting with "{" means the model was clearly attempting
		// the JSON envelope and it came out broken (most often: cut off
		// mid-object by a max_tokens budget) — showing that raw fragment as
		// if it were the chat reply is worse than an error.
		if strings.HasPrefix(cleaned, "{") {
			return nil, fmt.Errorf("%w: model's JSON response was malformed or cut off: %v", ErrInvalidOutput, err)
		}
		// Otherwise this is a plain conversational follow-up where the model
		// dropped the JSON envelope entirely and just answered in prose.
		// Treat that prose as the reply instead of failing the turn.
		log.Printf("agent: model dropped the JSON envelope, falling back to its raw text as the reply")
		return &agentTurn{Reply: trimmed}, nil
	}
	if strings.TrimSpace(turn.Reply) == "" {
		return nil, fmt.Errorf("%w: reply must not be empty", ErrInvalidOutput)
	}
	if turn.Estimate != nil {
		if err := turn.Estimate.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
		}
		if adjusted := turn.Estimate.ReconcileWithSubtasks(); adjusted {
			log.Printf("agent: model's total estimate did not match the sum of its subtasks, corrected to %.1f-%.1fh",
				turn.Estimate.EstimatedHoursMin, turn.Estimate.EstimatedHoursMax)
		}
	}

	return &turn, nil
}
