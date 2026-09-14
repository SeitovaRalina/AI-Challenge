package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// CompressionEvent records one history-compression fold, kept purely for
// demo/audit visibility — so a reviewer scrolling the chat (or reading logs)
// can see exactly when compression happened, how much it folded, and what
// the resulting summary actually said, not just infer it from token counts.
// FoldEnd is the Messages index the fold advanced SummarizedThrough to; the
// frontend anchors this event's notice right before that index, so it always
// renders at the exact point in history where the fold happened, even after
// a reload (Messages itself is never reordered or trimmed).
type CompressionEvent struct {
	FoldEnd         int       `json:"fold_end"`
	FoldedCount     int       `json:"folded_count"`
	SummarizedTotal int       `json:"summarized_total"`
	Summary         string    `json:"summary"`
	CreatedAt       time.Time `json:"created_at"`
	Manual          bool      `json:"manual"`
}

// summarizationSystemPrompt asks for a plain-text, mergeable summary — not
// the {"reply","estimate"} envelope agentSystemPrompt requires, since this
// call's output is consumed as raw text (folded into Chat.Summary), never
// parsed as JSON.
const summarizationSystemPrompt = `You are compressing an older part of a conversation between a user and a software-task-estimation assistant, so that part can be dropped from future prompts without losing anything important.

Produce a single, concise summary in Russian (a short paragraph, a few sentences), capturing: what was discussed, what was decided or asked, and any concrete numbers, constraints, or open questions mentioned. Precise current numeric estimates do not need to be preserved here — they are tracked separately and always sent in full.

If an existing summary is provided, merge it with the new segment into one updated summary: consolidate, do not just append the new part after the old one, and do not restate it as "previously, the summary said...".

Output plain text only: no JSON, no markdown formatting, no headers.`

// summarizeHistory asks the LLM to fold priorSummary (if any) and segment
// into one updated summary. It never mutates chat state itself — the caller
// (runCompression) commits the result under the lock.
func (a *Agent) summarizeHistory(ctx context.Context, priorSummary string, segment []AgentMessage) (string, *TokenUsage, error) {
	messages := make([]chatMessage, 0, len(segment)+3)
	messages = append(messages, chatMessage{Role: "system", Content: summarizationSystemPrompt})
	if priorSummary != "" {
		messages = append(messages, chatMessage{
			Role:    "system",
			Content: "Текущее резюме более ранней части диалога:\n" + priorSummary,
		})
	}
	for _, m := range segment {
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, chatMessage{
		Role:    "user",
		Content: "Сформируй одно обновлённое резюме, объединив текущее резюме (если оно было) с этим фрагментом диалога.",
	})

	completion, err := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, 0, nil)
	if err != nil {
		return "", nil, err
	}
	summary := strings.TrimSpace(completion.Choices[0].Message.Content)
	if summary == "" {
		return "", nil, fmt.Errorf("%w: summarization returned empty text", ErrInvalidOutput)
	}
	return summary, tokenUsageFrom(completion.Usage), nil
}

// runCompression does the actual LLM call and commit for both the automatic
// trigger and the manual /compress command: summarize [priorSummary, segment)
// into an updated Summary, and advance SummarizedThrough to foldEnd. Its own
// (possibly slow) LLM call happens with no lock held; only the final commit
// briefly re-acquires it — the same snapshot/unlock/slow-call/relock pattern
// PostMessage's own main call uses, so this never blocks other chats.
func (a *Agent) runCompression(ctx context.Context, chatID, priorSummary string, segment []AgentMessage, foldEnd int, manual bool) (*CompressionEvent, error) {
	updated, usage, err := a.summarizeHistory(ctx, priorSummary, segment)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	chat, ok := a.chats[chatID]
	if !ok {
		return nil, nil
	}
	chat.Summary = updated
	chat.SummarizedThrough = foldEnd
	chat.addUsage(usage)
	event := CompressionEvent{
		FoldEnd:         foldEnd,
		FoldedCount:     len(segment),
		SummarizedTotal: foldEnd,
		Summary:         updated,
		CreatedAt:       time.Now(),
		Manual:          manual,
	}
	chat.CompressionEvents = append(chat.CompressionEvents, event)
	log.Printf("agent: chat %s: compressed %d message(s) into summary (manual=%t, %d raw message(s) remain)\n  summary: %s",
		chatID, len(segment), manual, len(chat.Messages)-foldEnd, truncateForLog(updated))
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after compression: %v", chatID, err)
	}
	return &event, nil
}

// compressHistoryIfDue is the automatic trigger, called after every turn on
// a rolling_summary chat: once the raw (not-yet-summarized) tail has grown
// past 2*historyKeepLastN messages, it folds everything except the last
// historyKeepLastN back into the rolling Summary. This keeps the raw tail
// actually sent to the model between N and 2N messages at all times, instead
// of growing without bound. A failure here is logged and swallowed — it must
// never fail the user's own turn, which has already completed by the time
// this runs; the next trigger will simply try again with a larger segment.
func (a *Agent) compressHistoryIfDue(ctx context.Context, chatID string) *CompressionEvent {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok || chat.ContextStrategy != StrategyRollingSummary || a.historyKeepLastN <= 0 {
		a.mu.Unlock()
		return nil
	}
	n := a.historyKeepLastN
	total := len(chat.Messages)
	summarizedThrough := chat.SummarizedThrough
	if summarizedThrough > total {
		summarizedThrough = 0
	}
	if total-summarizedThrough <= 2*n {
		a.mu.Unlock()
		return nil
	}
	foldEnd := total - n
	segment := append([]AgentMessage(nil), chat.Messages[summarizedThrough:foldEnd]...)
	priorSummary := chat.Summary
	a.mu.Unlock()

	event, err := a.runCompression(ctx, chatID, priorSummary, segment, foldEnd, false)
	if err != nil {
		log.Printf("agent: chat %s: auto history compression failed, keeping previous summary: %v", chatID, err)
		return nil
	}
	return event
}

// ForceCompress runs the same fold immediately for the /compress command,
// ignoring the 2N auto-trigger threshold — it only requires that the chat is
// currently on the rolling_summary strategy and has at least one message
// beyond the last historyKeepLastN to fold in. Returns false, nil (not an
// error) when there's nothing to compress yet.
func (a *Agent) ForceCompress(ctx context.Context, chatID string) (bool, error) {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok {
		a.mu.Unlock()
		return false, ErrChatNotFound
	}
	if chat.ContextStrategy != StrategyRollingSummary {
		a.mu.Unlock()
		return false, ErrWrongStrategy
	}
	n := a.historyKeepLastN
	if n <= 0 {
		a.mu.Unlock()
		return false, nil
	}
	total := len(chat.Messages)
	summarizedThrough := chat.SummarizedThrough
	if summarizedThrough > total {
		summarizedThrough = 0
	}
	if total-summarizedThrough <= n {
		a.mu.Unlock()
		return false, nil
	}
	foldEnd := total - n
	segment := append([]AgentMessage(nil), chat.Messages[summarizedThrough:foldEnd]...)
	priorSummary := chat.Summary
	a.mu.Unlock()

	if _, err := a.runCompression(ctx, chatID, priorSummary, segment, foldEnd, true); err != nil {
		return false, err
	}
	return true, nil
}
