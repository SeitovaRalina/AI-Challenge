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

Given the project's current known facts (a JSON object, possibly with empty fields), its current hard invariants (a separate JSON array, tracked elsewhere — see below), and the latest exchange from ONE of its chats (one user message and the assistant's reply), return the UPDATED complete set of facts as a single JSON object: {"known_stack": ["..."], "notes": ["..."]}.

Rules:
- "known_stack": short technology names (e.g. "Flutter", "Python", "PostgreSQL") the user has confirmed this project actually uses. No duplicates, no near-duplicates (update the existing entry instead of adding a slightly different phrasing of the same thing). NEVER also restate a stack item as a note ("uses MongoDB" is redundant with known_stack containing "MongoDB" — leave it out of notes entirely).
- "notes": SETTLED facts about the project that matter across MULTIPLE future tasks in it — team roles, business rules, architectural decisions actually made. NOT: small talk; anything specific to only the one task this exchange discussed (that belongs in that chat's own working memory, not here); open questions, unknowns, or missing information ("data structure is not yet defined", "current state is unknown" are NOT facts — never write them as notes, drop them instead).
- Never ADD a NEW note that only restates what an existing invariant (given below) already says, even in different words — invariants are tracked separately and are already enforced, so a fresh duplicate would just keep looking "true" on its own even after that invariant is later removed. This rule is about not ADDING new duplicates — it is NEVER grounds to drop an existing note that happens to overlap an invariant; keep every existing note that is still valid exactly as the "merge, don't accumulate" rule above already says, invariant overlap or not.
- Be extremely conservative about adding a note at all: most exchanges reveal nothing project-wide and should leave "notes" completely unchanged. A note describes a standing decision someone could rely on next month in a different chat — not a restatement of what this one message said.
- Merge, don't accumulate: before adding a note, check whether an existing one already covers it (even loosely) — update that one instead of adding a near-duplicate. Keep existing entries that are still valid, drop one the user explicitly retracted. Never invent a fact the exchange didn't actually establish.
- All text values (known_stack items, notes) are in Russian, regardless of what language the exchange itself was in.

Output ONLY that JSON object: no markdown fences, no commentary before or after it.`

// updateProjectMemory asks the LLM to fold one exchange into the project's
// prior known_stack/notes. currentInvariants is given as read-only context
// (never returned/modified here — see memory_invariants.go's own call) so
// this prompt can avoid restating something already tracked as a hard
// invariant, which would otherwise keep looking true in Notes even after
// that invariant is later removed. It never mutates state itself — the
// caller (updateProjectMemoryAfterTurn) commits the result under the lock.
func (a *Agent) updateProjectMemory(ctx context.Context, priorStack, priorNotes, currentInvariants []string, userMessage, assistantReply string) (knownStack, notes []string, usage *TokenUsage, err error) {
	prior := struct {
		KnownStack []string `json:"known_stack"`
		Notes      []string `json:"notes"`
	}{KnownStack: priorStack, Notes: priorNotes}
	encodedPrior := "{}"
	if encoded, encodeErr := json.Marshal(prior); encodeErr == nil {
		encodedPrior = string(encoded)
	}
	encodedInvariants := "[]"
	if encoded, encodeErr := json.Marshal(currentInvariants); encodeErr == nil {
		encodedInvariants = string(encoded)
	}

	messages := []chatMessage{
		{Role: "system", Content: projectMemorySystemPrompt},
		{Role: "system", Content: "Текущая память проекта: " + encodedPrior},
		{Role: "system", Content: "Текущие инварианты проекта (не трогай их, только не дублируй в notes): " + encodedInvariants},
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
	currentInvariants := project.Invariants
	a.mu.Unlock()

	knownStack, notes, usage, err := a.updateProjectMemory(ctx, priorStack, priorNotes, currentInvariants, userMessage, assistantReply)
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
