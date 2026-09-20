package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// projectMemorySystemPrompt asks for the project's long-term memory —
// known_stack and notes — consolidated across every chat in the project, not
// just the one this turn happened in. There is no compression pass for this
// list (unlike rolling_summary's history folding), so the discipline against
// unbounded growth is entirely in this prompt: update/merge an existing
// entry instead of appending a near-duplicate.
const projectMemorySystemPrompt = `You maintain the long-term memory of a software project, shared across every task-estimation conversation ("chat") that belongs to it.

Given the project's current known facts (a JSON object, possibly with empty fields) and the latest exchange from ONE of its chats (one user message and the assistant's reply), return the UPDATED complete set of facts as a single JSON object: {"known_stack": ["..."], "notes": ["..."]}.

Rules:
- "known_stack": short technology names (e.g. "Flutter", "Python", "PostgreSQL") the user has confirmed this project actually uses. No duplicates, no near-duplicates (update the existing entry instead of adding a slightly different phrasing of the same thing).
- "notes": short factual statements about the project that matter for estimating future tasks in it (roles, constraints, past decisions) — not small talk, not anything specific to only this one task (that belongs in that chat's own working memory, not here).
- Merge: keep existing entries that are still valid, add new ones the exchange revealed, consolidate one that changed, drop one the user explicitly retracted. Never invent a fact the exchange didn't actually establish.

Output ONLY that JSON object: no markdown fences, no commentary before or after it.`

// updateProjectMemory asks the LLM to fold one exchange into the project's
// prior known_stack/notes. It never mutates state itself — the caller
// (updateProjectMemoryAfterTurn) commits the result under the lock.
func (a *Agent) updateProjectMemory(ctx context.Context, priorStack, priorNotes []string, userMessage, assistantReply string) (knownStack, notes []string, usage *TokenUsage, err error) {
	prior := struct {
		KnownStack []string `json:"known_stack"`
		Notes      []string `json:"notes"`
	}{KnownStack: priorStack, Notes: priorNotes}
	encodedPrior := "{}"
	if encoded, encodeErr := json.Marshal(prior); encodeErr == nil {
		encodedPrior = string(encoded)
	}

	messages := []chatMessage{
		{Role: "system", Content: projectMemorySystemPrompt},
		{Role: "system", Content: "Текущая память проекта: " + encodedPrior},
		{Role: "user", Content: userMessage},
		{Role: "assistant", Content: assistantReply},
		{Role: "user", Content: "Обнови память проекта по правилам выше и верни JSON-объект целиком."},
	}

	// Bounded for the same reason as updateTaskMemory (see its comment) —
	// 500 was cutting the JSON off mid-object; 2000 leaves room for hidden
	// reasoning tokens plus the JSON itself.
	const projectMemoryMaxTokens = 2000
	completion, callErr := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, projectMemoryMaxTokens, nil)
	if callErr != nil {
		return nil, nil, nil, callErr
	}

	var updated struct {
		KnownStack []string `json:"known_stack"`
		Notes      []string `json:"notes"`
	}
	if unmarshalErr := json.Unmarshal([]byte(stripCodeFences(completion.Choices[0].Message.Content)), &updated); unmarshalErr != nil {
		return nil, nil, nil, fmt.Errorf("%w: project memory update did not return a JSON object: %v", ErrInvalidOutput, unmarshalErr)
	}
	return updated.KnownStack, updated.Notes, tokenUsageFrom(completion.Usage), nil
}

// updateProjectMemoryAfterTurn runs updateProjectMemory and commits its
// result onto the PROJECT (not the chat) — visible to every chat in that
// project afterward, not just chatID. Token usage is still billed to chatID,
// same as every other side-call, since Project has no cost fields of its
// own. A failure here is logged and swallowed, never fails the user's turn.
func (a *Agent) updateProjectMemoryAfterTurn(ctx context.Context, chatID, projectID, userMessage, assistantReply string) *Project {
	a.mu.Lock()
	project, ok := a.projects[projectID]
	if !ok {
		a.mu.Unlock()
		return nil
	}
	priorStack := project.KnownStack
	priorNotes := project.Notes
	a.mu.Unlock()

	knownStack, notes, usage, err := a.updateProjectMemory(ctx, priorStack, priorNotes, userMessage, assistantReply)
	if err != nil {
		log.Printf("agent: project %s: memory update failed, keeping previous: %v", projectID, err)
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	project, ok = a.projects[projectID]
	if !ok {
		return nil
	}
	project.KnownStack = knownStack
	project.Notes = notes
	if err := a.projectStore.Save(project); err != nil {
		log.Printf("agent: failed to persist project %s after memory update: %v", projectID, err)
	}
	if chat, ok := a.chats[chatID]; ok {
		chat.addUsage(usage)
		if err := a.store.Save(chat); err != nil {
			log.Printf("agent: failed to persist chat %s after project memory update: %v", chatID, err)
		}
	}
	log.Printf("agent: project %s: memory updated (%d stack item(s), %d note(s))", projectID, len(knownStack), len(notes))
	return projectCopy(project)
}
