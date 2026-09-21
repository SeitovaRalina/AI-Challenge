package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// invariantsSystemPrompt asks for the project's hard invariants — the
// day-14 counterpart to projectMemorySystemPrompt, deliberately stricter:
// KnownStack/Notes are reference memory the model can reshuffle freely,
// while an invariant is a rule the agent will later refuse to violate, so a
// false positive here is far more costly than a false positive in a note.
const invariantsSystemPrompt = `You maintain the hard invariants of a software project — constraints the assistant must NEVER violate when proposing or estimating work, shared across every task-estimation conversation ("chat") that belongs to this project.

Given the project's current invariants (a JSON array, possibly empty) and the latest exchange from ONE of its chats (one user message and the assistant's reply), return the UPDATED complete array as a single JSON object: {"invariants": ["..."]}.

Rules:
- Add an invariant ONLY when the user explicitly, firmly settled a constraint or decision in THIS exchange — a stack choice, an architecture decision, a business rule, a scope/budget limit stated as final. "We'll only use Go on the backend", "no third-party auth providers", "this task must not exceed 40 hours" all qualify.
- NEVER add one from a passing remark, a question, a hypothetical, something the assistant merely suggested and the user did not confirm, or something already covered (even loosely) by an existing entry.
- Each invariant is a short, self-contained, unambiguous rule in Russian, stated as a constraint ("Бэкенд только на Go", not "мы обсуждали Go").
- NEVER remove or reword an existing entry yourself, even if this exchange seems to contradict it — conflicting requests are handled elsewhere, by refusing them; removing an invariant is a deliberate user action outside this call. Always return every prior entry unchanged, plus at most a small number of new ones.
- Be extremely conservative: most exchanges settle nothing project-wide and must leave the array completely unchanged.

Output ONLY that JSON object: no markdown fences, no commentary before or after it.`

// InvariantDiff describes how a project's invariants changed as a result of
// one turn — Added is the common case; Removed stays here for honesty (the
// diff is computed from actual before/after values) even though
// invariantsSystemPrompt forbids the model from ever producing it, so the
// only way this is populated in practice is a manual edit racing this call.
type InvariantDiff struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

// diffInvariants returns nil when before and after are the same set.
func diffInvariants(before, after []string) *InvariantDiff {
	beforeSet := make(map[string]bool, len(before))
	for _, v := range before {
		beforeSet[v] = true
	}
	afterSet := make(map[string]bool, len(after))
	for _, v := range after {
		afterSet[v] = true
	}

	var diff InvariantDiff
	for _, v := range after {
		if !beforeSet[v] {
			diff.Added = append(diff.Added, v)
		}
	}
	for _, v := range before {
		if !afterSet[v] {
			diff.Removed = append(diff.Removed, v)
		}
	}
	if len(diff.Added) == 0 && len(diff.Removed) == 0 {
		return nil
	}
	return &diff
}

// updateInvariants asks the LLM to fold one exchange into the project's
// prior invariants. It never mutates state itself — the caller
// (updateInvariantsAfterTurn) commits the result under the lock.
func (a *Agent) updateInvariants(ctx context.Context, prior []string, userMessage, assistantReply string) (invariants []string, usage *TokenUsage, err error) {
	priorPayload := struct {
		Invariants []string `json:"invariants"`
	}{Invariants: prior}
	encodedPrior := "{}"
	if encoded, encodeErr := json.Marshal(priorPayload); encodeErr == nil {
		encodedPrior = string(encoded)
	}

	messages := []chatMessage{
		{Role: "system", Content: invariantsSystemPrompt},
		{Role: "system", Content: "Текущие инварианты проекта: " + encodedPrior},
		{Role: "user", Content: userMessage},
		{Role: "assistant", Content: assistantReply},
		{Role: "user", Content: "Обнови инварианты по правилам выше и верни JSON-объект целиком."},
	}

	// Same bound as updateProjectMemory — see its own comment.
	const invariantsMaxTokens = 2000
	completion, callErr := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, invariantsMaxTokens, nil)
	if callErr != nil {
		return nil, nil, callErr
	}

	var updated struct {
		Invariants []string `json:"invariants"`
	}
	if unmarshalErr := json.Unmarshal([]byte(stripCodeFences(completion.Choices[0].Message.Content)), &updated); unmarshalErr != nil {
		return nil, nil, fmt.Errorf("%w: invariants update did not return a JSON object: %v", ErrInvalidOutput, unmarshalErr)
	}
	return updated.Invariants, tokenUsageFrom(completion.Usage), nil
}

// updateInvariantsAfterTurn runs updateInvariants and commits its result
// onto the PROJECT (not the chat) — visible to every chat in that project
// afterward. Token usage is billed to chatID, same as every other side-call.
// A failure here is logged and swallowed, never fails the user's turn.
// Returns (nil, nil) when the invariants list did not actually change.
func (a *Agent) updateInvariantsAfterTurn(ctx context.Context, chatID, projectID, userMessage, assistantReply string) (*Project, *InvariantDiff) {
	a.mu.Lock()
	project, ok := a.projects[projectID]
	if !ok {
		a.mu.Unlock()
		return nil, nil
	}
	prior := project.Invariants
	a.mu.Unlock()

	updated, usage, err := a.updateInvariants(ctx, prior, userMessage, assistantReply)
	if err != nil {
		log.Printf("agent: project %s: invariants update failed, keeping previous: %v", projectID, err)
		return nil, nil
	}

	diff := diffInvariants(prior, updated)
	if diff == nil {
		return nil, nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	project, ok = a.projects[projectID]
	if !ok {
		return nil, nil
	}
	project.Invariants = updated
	if err := a.projectStore.Save(project); err != nil {
		log.Printf("agent: failed to persist project %s after invariants update: %v", projectID, err)
	}
	if chat, ok := a.chats[chatID]; ok {
		chat.addUsage(usage)
		if err := a.store.Save(chat); err != nil {
			log.Printf("agent: failed to persist chat %s after invariants update: %v", chatID, err)
		}
	}
	log.Printf("agent: project %s: invariants updated (+%d/-%d)", projectID, len(diff.Added), len(diff.Removed))
	return projectCopy(project), diff
}
