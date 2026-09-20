package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// profileSystemPrompt asks for the single global user profile — name, stack,
// style, format, constraints — folded in from one exchange. Unlike
// known_stack/notes (facts about a project), every field here describes the
// USER, not the work, so the bar for writing anything is "the user
// explicitly said this about themselves or how they want to be treated,"
// never an inference from the exchange's topic or tone.
const profileSystemPrompt = `You maintain a single global user profile for a task-estimation assistant — personalization that applies to every conversation, not any one chat or project.

Given the current profile (a JSON object, possibly with empty fields) and the latest exchange (one user message and the assistant's reply), return the UPDATED complete profile as a single JSON object: {"name": "...", "stack": ["..."], "style": "...", "format": "...", "constraints": ["..."]}.

Rules:
- "name": how to address the user. Update ONLY when the user explicitly states their name or asks to be called something ("меня зовут ...", "зови меня ..."). NEVER infer, guess, or derive it from anything else (a signature, an email-like string, the topic). Leave unchanged if the exchange didn't establish it.
- "stack": short technology/tool names the user says THEY personally usually work with (e.g. "Flutter", "Python", "React") — a fact about the user, update when they state it, same as a name. NOT the stack of the one task being discussed in this exchange (that belongs in that task's own estimate, not here) — only what the user describes as their own general, usual stack. No duplicates or near-duplicates; merge into an existing entry instead of adding a slightly different phrasing.
- "style": the tone/manner of conversation the user explicitly asked for (e.g. "неформальный, на ты", "сухо и по делу"). Update ONLY when the user explicitly requests a tone or manner — never infer it from how the user themselves happens to write.
- "format": the structure/length of answers the user explicitly asked for (e.g. "коротко, без вступлений", "развёрнуто, с примерами", "списками, не прозой"). Update ONLY on an explicit request about how answers should be shaped.
- "constraints": other settled, explicit personal rules the user stated (e.g. "не используй оценку в днях, только в часах", "не используй эмодзи"). Only add one the exchange actually established; never invent one.
- This is scoped across ALL future conversations — be conservative. Most exchanges establish nothing about the user's profile and should leave every field unchanged.
- Merge, don't accumulate: before adding a stack item or constraint, check whether an existing one already covers it — update that one instead of adding a near-duplicate.
- All text values (name, style, format, constraints; stack entries keep their usual spelling, e.g. "React") are in Russian, regardless of what language the exchange itself was in — except "name" itself, which is written exactly as the user gave it.

Output ONLY that JSON object: no markdown fences, no commentary before or after it.`

// updateProfile asks the LLM to fold one exchange into the prior global
// profile. It never mutates state itself — the caller (updateProfileAfterTurn)
// commits the result under the lock.
func (a *Agent) updateProfile(ctx context.Context, prior *UserProfile, userMessage, assistantReply string) (*UserProfile, *TokenUsage, error) {
	encodedPrior := "{}"
	if prior != nil {
		if encoded, err := json.Marshal(prior); err == nil {
			encodedPrior = string(encoded)
		}
	}

	messages := []chatMessage{
		{Role: "system", Content: profileSystemPrompt},
		{Role: "system", Content: "Текущий профиль: " + encodedPrior},
		{Role: "user", Content: userMessage},
		{Role: "assistant", Content: assistantReply},
		{Role: "user", Content: "Обнови профиль по правилам выше и верни JSON-объект целиком."},
	}

	// Bounded for the same reason as updateTaskMemory/updateProjectMemory —
	// 500 was cutting the JSON off mid-object; 2000 leaves room for hidden
	// reasoning tokens plus the JSON itself.
	const profileMaxTokens = 2000
	completion, err := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, profileMaxTokens, nil)
	if err != nil {
		return nil, nil, err
	}

	var updated UserProfile
	if err := json.Unmarshal([]byte(stripCodeFences(completion.Choices[0].Message.Content)), &updated); err != nil {
		return nil, nil, fmt.Errorf("%w: profile update did not return a JSON object: %v", ErrInvalidOutput, err)
	}
	if updated.Stack == nil {
		updated.Stack = []string{}
	}
	if updated.Constraints == nil {
		updated.Constraints = []string{}
	}
	return &updated, tokenUsageFrom(completion.Usage), nil
}

// updateProfileAfterTurn runs updateProfile and commits its result onto the
// single global profile (not any particular chat), called after every real
// turn of every non-lab chat regardless of ProjectID — the profile applies
// everywhere, unlike Project memory which only exists within a project.
// Token usage is billed to chatID, same as project memory, since UserProfile
// has no cost fields of its own. A failure here is logged and swallowed,
// exactly like the other memory-layer side-calls: it must never fail the
// user's own turn, which has already completed by the time this runs.
func (a *Agent) updateProfileAfterTurn(ctx context.Context, chatID, userMessage, assistantReply string) *UserProfile {
	a.mu.Lock()
	prior := a.profile
	a.mu.Unlock()

	updated, usage, err := a.updateProfile(ctx, prior, userMessage, assistantReply)
	if err != nil {
		log.Printf("agent: profile update failed, keeping previous: %v", err)
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.profile = updated
	if err := a.profileStore.Save(a.profile); err != nil {
		log.Printf("agent: failed to persist profile after update: %v", err)
	}
	if chat, ok := a.chats[chatID]; ok {
		chat.addUsage(usage)
		if err := a.store.Save(chat); err != nil {
			log.Printf("agent: failed to persist chat %s after profile update: %v", chatID, err)
		}
	}
	log.Printf("agent: profile updated (name=%q, %d stack item(s), %d constraint(s))", updated.Name, len(updated.Stack), len(updated.Constraints))
	return profileCopy(a.profile)
}
