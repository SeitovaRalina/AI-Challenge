package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// factsSystemPrompt asks for a plain JSON object of facts — merged, not
// appended, the same "consolidate, don't just concatenate" rule
// summarizationSystemPrompt uses for rolling_summary, so the fact table stays
// small and current instead of accumulating stale or contradicted entries.
const factsSystemPrompt = `You maintain a compact key-value memory of important facts from a conversation between a user and a software-task-estimation assistant.

Given the current facts (a JSON object, possibly empty) and the latest exchange (one user message and the assistant's reply), return the UPDATED complete set of facts as a single JSON object: {"key": "value", ...}.

Rules:
- Merge: keep existing facts that are still valid, add new ones the exchange revealed, update ones that changed, drop ones the user explicitly retracted.
- Keys are short snake_case labels (e.g. "goal", "constraints", "budget", "deadline", "preferences", "decisions"). Values are short factual statements in Russian.
- Only keep facts that matter for continuing the task later — goals, constraints, preferences, decisions, agreements — not small talk or restated questions.
- This memory does not persist beyond this one conversation — never invent a fact the exchange didn't actually establish.

Output ONLY that JSON object: no markdown fences, no commentary before or after it.`

// updateFacts asks the LLM to fold one exchange into priorFacts. It never
// mutates chat state itself — the caller (updateFactsAfterTurn) commits the
// result under the lock, mirroring the rolling_summary strategy's
// summarizeHistory/runCompression split.
func (a *Agent) updateFacts(ctx context.Context, priorFacts map[string]string, userMessage, assistantReply string) (map[string]string, *TokenUsage, error) {
	encodedFacts := "{}"
	if len(priorFacts) > 0 {
		if encoded, err := json.Marshal(priorFacts); err == nil {
			encodedFacts = string(encoded)
		}
	}

	messages := []chatMessage{
		{Role: "system", Content: factsSystemPrompt},
		{Role: "system", Content: "Текущие факты: " + encodedFacts},
		{Role: "user", Content: userMessage},
		{Role: "assistant", Content: assistantReply},
		{Role: "user", Content: "Обнови факты по правилам выше и верни JSON-объект целиком."},
	}

	completion, err := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, 0, nil)
	if err != nil {
		return nil, nil, err
	}

	var updated map[string]string
	if err := json.Unmarshal([]byte(stripCodeFences(completion.Choices[0].Message.Content)), &updated); err != nil {
		return nil, nil, fmt.Errorf("%w: facts update did not return a JSON object: %v", ErrInvalidOutput, err)
	}
	if updated == nil {
		updated = map[string]string{}
	}
	return updated, tokenUsageFrom(completion.Usage), nil
}

// updateFactsAfterTurn runs updateFacts and commits its result, called after
// every real turn (not a graceful/synthetic one — there's no real content to
// extract facts from) on a sticky_facts chat. A failure here is logged and
// swallowed, exactly like compressHistoryIfDue's: it must never fail the
// user's own turn, which has already completed by the time this runs.
// Returns nil facts on failure or on a chat that isn't (or is no longer)
// using this strategy, so the caller can tell "nothing changed" from "facts
// are now empty".
func (a *Agent) updateFactsAfterTurn(ctx context.Context, chatID, userMessage, assistantReply string) map[string]string {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok || chat.ContextStrategy != StrategyStickyFacts {
		a.mu.Unlock()
		return nil
	}
	priorFacts := chat.Facts
	a.mu.Unlock()

	updated, usage, err := a.updateFacts(ctx, priorFacts, userMessage, assistantReply)
	if err != nil {
		log.Printf("agent: chat %s: facts update failed, keeping previous facts: %v", chatID, err)
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	chat, ok = a.chats[chatID]
	if !ok {
		return nil
	}
	chat.Facts = updated
	chat.addUsage(usage)
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after facts update: %v", chatID, err)
	}
	log.Printf("agent: chat %s: facts updated (%d key(s))", chatID, len(updated))
	return updated
}
