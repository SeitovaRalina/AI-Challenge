package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// taskMemorySystemPrompt asks for the chat's working-memory object — the
// goal, agreed constraints, and answers gathered so far for THIS ONE task.
// Unlike factsSystemPrompt's free-form key-value map, this has a fixed
// shape. Constraints are settled facts/boundaries, not open questions —
// tracking "what's still unanswered" added little the raw short-term window
// didn't already cover (the model can just re-ask from the live
// conversation), while a decision silently forgotten once its message
// slides out of that window is a real risk worth guarding against.
const taskMemorySystemPrompt = `You maintain the working memory of ONE specific software-task-estimation conversation for a task-estimation assistant.

Given the current working memory (a JSON object, possibly with empty fields) and the latest exchange (one user message and the assistant's reply), return the UPDATED complete working memory as a single JSON object: {"goal": "...", "constraints": ["..."], "clarifying_answers": {"question": "answer", ...}}.

Rules:
- "goal": one short sentence naming what's being estimated. Empty string if not yet clear.
- "constraints": settled facts or boundaries the user has explicitly agreed for THIS task (e.g. "offline mode is not needed", "payments are out of scope for this pass") — decisions, not open questions. Only add one when the exchange actually settled it; never invent one.
- "clarifying_answers": short factual answers the user has actually given, keyed by a short label for the question they answer (not the full question text).
- This is scoped to THIS conversation only — never invent a goal, constraint, or answer the exchange didn't actually establish.
- Merge, don't accumulate: before adding a constraint, check whether an existing one already covers it — update that one instead of adding a near-duplicate.
- All text values (goal, constraints, clarifying_answers) are in Russian, regardless of what language the exchange itself was in.

Output ONLY that JSON object: no markdown fences, no commentary before or after it.`

// updateTaskMemory asks the LLM to fold one exchange into prior. It never
// mutates chat state itself — the caller (updateTaskMemoryAfterTurn) commits
// the result under the lock, mirroring updateFacts/updateFactsAfterTurn.
func (a *Agent) updateTaskMemory(ctx context.Context, prior *TaskMemory, userMessage, assistantReply string) (*TaskMemory, *TokenUsage, error) {
	encodedPrior := "{}"
	if prior != nil {
		if encoded, err := json.Marshal(prior); err == nil {
			encodedPrior = string(encoded)
		}
	}

	messages := []chatMessage{
		{Role: "system", Content: taskMemorySystemPrompt},
		{Role: "system", Content: "Текущая рабочая память: " + encodedPrior},
		{Role: "user", Content: userMessage},
		{Role: "assistant", Content: assistantReply},
		{Role: "user", Content: "Обнови рабочую память по правилам выше и верни JSON-объект целиком."},
	}

	// A bounded budget, not 0 (=unlimited): this is a small structured-JSON
	// extraction, never a long answer — leaving it unbounded lets a
	// reasoning-capable model spend an unpredictable, sometimes very long
	// time on hidden reasoning tokens before it ever emits the JSON, which
	// was observed pushing a turn's total latency past the handler's own
	// 60s budget (see postAgentMessageHandler). 500 was tried first and cut
	// the JSON off mid-object (finish_reason=length) whenever the model
	// spent part of that budget on hidden reasoning before the visible
	// output — 2000 leaves enough room for that plus the JSON itself while
	// still being far below "unlimited".
	const taskMemoryMaxTokens = 2000
	completion, err := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, taskMemoryMaxTokens, nil)
	if err != nil {
		return nil, nil, err
	}

	var updated TaskMemory
	if err := json.Unmarshal([]byte(stripCodeFences(completion.Choices[0].Message.Content)), &updated); err != nil {
		return nil, nil, fmt.Errorf("%w: task memory update did not return a JSON object: %v", ErrInvalidOutput, err)
	}
	// The model omitting an empty array/object key (valid JSON, but not what
	// Constraints/ClarifyingAnswers's own json tags assume) would otherwise
	// leave these as Go's nil zero value, which marshals back out as JSON
	// null instead of [] / {} — the same "null where the frontend expects an
	// array" hazard fixed in copyChat, guarded here at the source instead.
	if updated.Constraints == nil {
		updated.Constraints = []string{}
	}
	if updated.ClarifyingAnswers == nil {
		updated.ClarifyingAnswers = map[string]string{}
	}
	return &updated, tokenUsageFrom(completion.Usage), nil
}

// updateTaskMemoryAfterTurn runs updateTaskMemory and commits its result,
// called after every real turn (not a graceful/synthetic one) regardless of
// the chat's ContextStrategy — working memory is a layer independent of how
// history gets windowed/compressed. A failure here is logged and swallowed,
// exactly like updateFactsAfterTurn's: it must never fail the user's own
// turn, which has already completed by the time this runs.
func (a *Agent) updateTaskMemoryAfterTurn(ctx context.Context, chatID, userMessage, assistantReply string) *TaskMemory {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok {
		a.mu.Unlock()
		return nil
	}
	prior := chat.Task
	a.mu.Unlock()

	updated, usage, err := a.updateTaskMemory(ctx, prior, userMessage, assistantReply)
	if err != nil {
		log.Printf("agent: chat %s: task memory update failed, keeping previous: %v", chatID, err)
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	chat, ok = a.chats[chatID]
	if !ok {
		return nil
	}
	chat.Task = updated
	chat.addUsage(usage)
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after task memory update: %v", chatID, err)
	}
	log.Printf("agent: chat %s: task memory updated (%d constraint(s))", chatID, len(updated.Constraints))
	return updated
}

// UpdateChatTask overwrites a chat's working memory with an explicit,
// user-authored value — the manual counterpart to updateTaskMemoryAfterTurn's
// automatic per-turn extraction. See UpdateProjectMemory's doc comment for
// why this manual path exists at all.
func (a *Agent) UpdateChatTask(chatID string, task TaskMemory) (*TaskMemory, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}
	if task.Constraints == nil {
		task.Constraints = []string{}
	}
	if task.ClarifyingAnswers == nil {
		task.ClarifyingAnswers = map[string]string{}
	}
	chat.Task = &task
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after manual task edit: %v", chatID, err)
	}
	log.Printf("agent: chat %s: task memory manually edited (%d constraint(s))", chatID, len(task.Constraints))
	return chat.Task, nil
}
