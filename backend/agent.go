package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
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

// AgentMessage is one chat message as returned to the frontend. Role is
// "user" or "assistant"; the system prompt is never exposed. Usage holds the
// token accounting of the LLM call this message's turn produced — the same
// object on both the user and assistant message of one turn, since both were
// billed by that single call. It is nil for messages restored from before
// this field existed, and for the synthetic reply a context-overflow turn
// returns without ever calling the LLM.
type AgentMessage struct {
	Role      string      `json:"role"`
	Content   string      `json:"content"`
	CreatedAt time.Time   `json:"created_at"`
	Usage     *TokenUsage `json:"usage,omitempty"`
}

// Chat is one independent conversation the Agent holds in memory: its own
// message history and the latest estimate produced within it, isolated from
// every other chat. LastContextTokens is prompt_tokens + completion_tokens of
// the most recent LLM call — the full conversation-so-far AND the reply it
// just produced, since that reply is appended to history and will itself be
// sent back as part of the prompt on the next turn. It's what PostMessage
// compares against the agent's context token limit before spending another
// call, and what the UI shows as "context used" — using prompt_tokens alone
// would under-count by exactly the size of the last reply, since a reply
// that was just generated already occupies context space going forward, even
// though it was never itself sent as part of a prompt yet.
// CumulativeTotalTokens/CumulativeCostUsd are a running sum across every
// turn, for display only.
//
// Summary/SummarizedThrough implement Day 9's history compression: Messages
// is never trimmed or deleted (a chat's full raw history always survives a
// restart, per Day 7), but once it grows large, PostMessage only resends the
// last few messages verbatim — everything before index SummarizedThrough is
// instead represented by the rolling Summary text, kept up to date by its
// own periodic LLM call (see compressHistoryIfDue). CompressionEnabled is a
// per-chat, live-toggleable switch (not a global setting) so the same
// conversation can be compared with compression on and off without starting
// a new chat.
type Chat struct {
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	CreatedAt             time.Time         `json:"created_at"`
	Messages              []AgentMessage    `json:"messages"`
	Estimate              *EstimateResponse `json:"estimate"`
	LastContextTokens     int               `json:"last_context_tokens"`
	CumulativeTotalTokens int               `json:"cumulative_total_tokens"`
	CumulativeCostUsd     *float64          `json:"cumulative_cost_usd,omitempty"`
	Summary               string            `json:"summary,omitempty"`
	SummarizedThrough     int               `json:"summarized_through"`
	CompressionEnabled    bool              `json:"compression_enabled"`
	CompressionEvents     []CompressionEvent `json:"compression_events"`
}

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

// ChatSummary is a chat's identity without its message history, for listing.
type ChatSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

// Agent is the entity that owns every chat, encapsulating conversation state
// and the logic to turn a chat message into an LLM request and back. Handlers
// only translate HTTP to and from Agent's methods; they never call the LLM
// client directly.
type Agent struct {
	client            *LiteLLMClient
	store             *ChatStore
	contextTokenLimit int // 0 disables the pre-call overflow guard entirely

	historyKeepLastN          int  // 0 disables history compression entirely
	historyCompressionDefault bool // initial Chat.CompressionEnabled for new chats

	mu    sync.Mutex
	chats map[string]*Chat
	order []string // chat IDs, oldest first, for stable listing order
}

// NewAgent restores every chat store persisted so a restart continues each
// conversation exactly where it left off; a store read failure is logged and
// treated as an empty history rather than aborting startup. contextTokenLimit
// is the token budget PostMessage guards against before every LLM call (see
// its doc comment) and the denominator the frontend's context-usage bar uses;
// pass 0 to disable the guard and always call through to the LLM.
// historyKeepLastN is how many of the most recent messages every chat keeps
// "as is" once compression is due (see compressHistoryIfDue); pass 0 to
// disable compression entirely, for every chat, regardless of their own
// CompressionEnabled. historyCompressionDefault seeds new chats'
// CompressionEnabled — existing chats keep whatever they were last set to.
func NewAgent(client *LiteLLMClient, store *ChatStore, contextTokenLimit, historyKeepLastN int, historyCompressionDefault bool) *Agent {
	agent := &Agent{
		client:                    client,
		store:                     store,
		contextTokenLimit:         contextTokenLimit,
		historyKeepLastN:          historyKeepLastN,
		historyCompressionDefault: historyCompressionDefault,
		chats:                     make(map[string]*Chat),
	}

	chats, err := store.LoadAll()
	if err != nil {
		log.Printf("agent: failed to load persisted chats: %v", err)
		return agent
	}
	for _, chat := range chats {
		agent.chats[chat.ID] = chat
		agent.order = append(agent.order, chat.ID)
	}
	log.Printf("agent: restored %d chat(s) from %s", len(chats), store.dir)
	return agent
}

func newChatID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// CreateChat starts a new, empty conversation and returns its summary.
func (a *Agent) CreateChat() ChatSummary {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat := &Chat{
		ID:                 newChatID(),
		Title:              "Новый чат",
		CreatedAt:          time.Now(),
		Messages:           []AgentMessage{},
		CompressionEnabled: a.historyCompressionDefault,
	}
	a.chats[chat.ID] = chat
	a.order = append(a.order, chat.ID)

	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist new chat %s: %v", chat.ID, err)
	}
	log.Printf("agent: created chat %s", chat.ID)

	return chatSummary(chat)
}

// ListChats returns every chat's summary, oldest first.
func (a *Agent) ListChats() []ChatSummary {
	a.mu.Lock()
	defer a.mu.Unlock()

	summaries := make([]ChatSummary, 0, len(a.order))
	for _, id := range a.order {
		summaries = append(summaries, chatSummary(a.chats[id]))
	}
	return summaries
}

var ErrChatNotFound = fmt.Errorf("agent: chat not found")

// DeleteChat permanently removes a chat and its history.
func (a *Agent) DeleteChat(chatID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, ok := a.chats[chatID]; !ok {
		return ErrChatNotFound
	}
	delete(a.chats, chatID)
	for i, id := range a.order {
		if id == chatID {
			a.order = append(a.order[:i], a.order[i+1:]...)
			break
		}
	}
	log.Printf("agent: deleted chat %s", chatID)
	return a.store.Delete(chatID)
}

// RenameChat sets a chat's display title to a user-chosen value, so it no
// longer gets overwritten by the first-message auto-title.
func (a *Agent) RenameChat(chatID, title string) (ChatSummary, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return ChatSummary{}, ErrChatNotFound
	}
	chat.Title = title
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist renamed chat %s: %v", chat.ID, err)
	}
	return chatSummary(chat), nil
}

// GetChat returns one chat's full state (history and current estimate).
func (a *Agent) GetChat(chatID string) (*Chat, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}

	// Return a copy so the caller can't mutate agent state without the lock.
	copied := *chat
	copied.Messages = append([]AgentMessage(nil), chat.Messages...)
	return &copied, nil
}

// AgentReply is what one chat turn returns to the caller: the assistant's
// visible reply plus the chat's current estimate (nil until the first turn
// produces one), the token accounting of this turn's LLM call (nil if the
// context-overflow guard skipped it), and the chat's running token/cost
// totals so the frontend never has to re-derive them from message history.
type AgentReply struct {
	Reply                     string            `json:"reply"`
	Estimate                  *EstimateResponse `json:"estimate"`
	Title                     string            `json:"title"`
	Usage                     *TokenUsage       `json:"usage"`
	UserMessageCreatedAt      time.Time         `json:"user_message_created_at"`
	AssistantMessageCreatedAt time.Time         `json:"assistant_message_created_at"`
	LastContextTokens         int               `json:"last_context_tokens"`
	CumulativeTotalTokens     int               `json:"cumulative_total_tokens"`
	CumulativeCostUsd         *float64          `json:"cumulative_cost_usd,omitempty"`
	ContextTokenLimit         int               `json:"context_token_limit"`
	CompressionEnabled        bool              `json:"compression_enabled"`
	HistoryKeepLastN          int               `json:"history_keep_last_n"`
	SummarizedMessageCount    int               `json:"summarized_message_count"`
	RawMessageCount           int               `json:"raw_message_count"`
	NewCompressionEvent       *CompressionEvent `json:"new_compression_event,omitempty"`
}

// PostMessage appends the user's message to chatID's history, asks the LLM
// for a reply using the full conversation so far, and stores the assistant's
// reply back into that same chat's history — never another chat's.
func (a *Agent) PostMessage(ctx context.Context, chatID, userMessage string) (*AgentReply, error) {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok {
		a.mu.Unlock()
		return nil, ErrChatNotFound
	}
	// Snapshot history and the current estimate under the lock, then release
	// it for the (slow) LLM call — a chat is only ever driven by one user, so
	// this is safe.
	history := append([]AgentMessage(nil), chat.Messages...)
	currentEstimate := chat.Estimate
	lastContextTokens := chat.LastContextTokens
	compressionEnabled := chat.CompressionEnabled
	summary := chat.Summary
	summarizedThrough := chat.SummarizedThrough
	a.mu.Unlock()

	log.Printf("agent: chat %s: turn %d, message length %d", chatID, len(history)/2+1, len(userMessage))

	userSentAt := time.Now()

	// Pre-call overflow guard: the previous turn's prompt_tokens PLUS its own
	// completion_tokens is what's actually sitting in history right now — the
	// reply the model just wrote is appended to the conversation and will
	// itself be sent back as part of the prompt on the next call, so it
	// already occupies context space even though it was never sent as a
	// prompt yet. Using prompt_tokens alone would under-count by exactly the
	// size of the last reply. This turn's history is at most a couple of
	// messages larger than that — close enough to treat as this turn's
	// starting budget. Rather than let the model write an unbounded reply and
	// only check the total afterward (which is how the total can end up past
	// the limit instead of at it), we cap the real call's max_tokens to
	// whatever room is left — the same way a real model's context window
	// bounds a single turn's completion — so the upstream API itself
	// truncates (finish_reason "length") if the answer would need more room
	// than remains. Below minCompletionBudget there isn't enough room left
	// for a coherent reply (the model couldn't even open the JSON envelope),
	// so that case still skips the call entirely, exactly like a real API's
	// upfront context_length_exceeded rejection.
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
	// Day 9 history compression: once compressHistoryIfDue has folded an
	// older segment into Summary, only the raw tail after SummarizedThrough
	// is resent verbatim — Summary stands in for everything before it. This
	// is what keeps the prompt (and therefore LastContextTokens) from
	// growing linearly forever; without it, every turn resends the entire
	// history, as before Day 9.
	raw := history
	if compressionEnabled {
		if summarizedThrough > len(history) {
			summarizedThrough = 0
		}
		raw = history[summarizedThrough:]
		if summary != "" {
			messages = append(messages, chatMessage{
				Role: "system",
				Content: "Резюме более ранней части диалога (используй как контекст; " +
					"для точных чисел оценки полагайся на «Текущая актуальная оценка» выше, а не на резюме): " + summary,
			})
		}
	}
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
		// either. This is NOT the dialog running out of room (the history
		// itself may be nowhere near the limit) — it's this one reply that
		// needed more than the remaining per-turn budget, so it gets its
		// own honest message instead of the "context limit reached" one.
		if finishReason == "length" {
			log.Printf("agent: chat %s: truncated response was unusable (finish_reason=length, %d-token budget): %v", chatID, maxTokens, err)
			// The call genuinely happened and cost real tokens — usage is
			// still populated even though the content came out unusable, so
			// the chat's running totals must reflect it (chat.LastContextTokens
			// included) instead of staying frozen at the stale pre-call value.
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

	chat.Messages = append(chat.Messages,
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
		chat.CumulativeTotalTokens += usage.TotalTokens
		if usage.CostUsd != nil {
			if chat.CumulativeCostUsd == nil {
				cost := *usage.CostUsd
				chat.CumulativeCostUsd = &cost
			} else {
				*chat.CumulativeCostUsd += *usage.CostUsd
			}
		}
	}

	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}

	agentReply := &AgentReply{
		Reply:                     turn.Reply,
		Estimate:                  chat.Estimate,
		Title:                     chat.Title,
		Usage:                     usage,
		UserMessageCreatedAt:      userSentAt,
		AssistantMessageCreatedAt: assistantSentAt,
		LastContextTokens:         chat.LastContextTokens,
		CumulativeTotalTokens:     chat.CumulativeTotalTokens,
		CumulativeCostUsd:         chat.CumulativeCostUsd,
		ContextTokenLimit:         a.contextTokenLimit,
		CompressionEnabled:        chat.CompressionEnabled,
		HistoryKeepLastN:          a.historyKeepLastN,
		SummarizedMessageCount:    chat.SummarizedThrough,
		RawMessageCount:           len(chat.Messages) - chat.SummarizedThrough,
	}
	a.mu.Unlock()

	// Runs its own (possibly slow) LLM call outside the lock just released,
	// so a compression call for this chat never blocks any other chat — see
	// compressHistoryIfDue's doc comment for why this is safe to call here.
	// agentReply's compression fields were captured BEFORE this call, so if
	// it actually folds something, refresh them — otherwise this turn's own
	// response would understate what just happened to its own chat.
	if event := a.compressHistoryIfDue(ctx, chatID); event != nil {
		agentReply.NewCompressionEvent = event
		a.mu.Lock()
		if chat, ok := a.chats[chatID]; ok {
			agentReply.SummarizedMessageCount = chat.SummarizedThrough
			agentReply.RawMessageCount = len(chat.Messages) - chat.SummarizedThrough
			agentReply.CumulativeTotalTokens = chat.CumulativeTotalTokens
			agentReply.CumulativeCostUsd = chat.CumulativeCostUsd
		}
		a.mu.Unlock()
	}

	return agentReply, nil
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
// the model provider (finish_reason "length") into something unusable. This
// is a different situation from contextFullReplyText — the dialog is not
// full, this specific answer just didn't fit — so it gets its own honest
// wording instead of reusing "context limit reached" (which would be
// misleading, e.g. "882 / 1200" while insisting the limit was hit).
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
// or an outright upstream rejection). When a call DID happen but its
// content came out unusable (a truncated response that couldn't be
// parsed), pass its real usage: the request genuinely happened and cost
// real tokens, so the chat's running totals must reflect it.
func (a *Agent) finishGracefulTurn(ctx context.Context, chat *Chat, userMessage, reply string, userSentAt time.Time, usage *TokenUsage) (*AgentReply, error) {
	assistantSentAt := time.Now()

	a.mu.Lock()

	chat.Messages = append(chat.Messages,
		AgentMessage{Role: "user", Content: userMessage, CreatedAt: userSentAt, Usage: usage},
		AgentMessage{Role: "assistant", Content: reply, CreatedAt: assistantSentAt, Usage: usage},
	)
	if chat.Title == "Новый чат" {
		chat.Title = chatTitleFrom(userMessage)
	}
	if usage != nil {
		chat.LastContextTokens = usage.TotalTokens
		chat.CumulativeTotalTokens += usage.TotalTokens
		if usage.CostUsd != nil {
			if chat.CumulativeCostUsd == nil {
				cost := *usage.CostUsd
				chat.CumulativeCostUsd = &cost
			} else {
				*chat.CumulativeCostUsd += *usage.CostUsd
			}
		}
	}
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}

	chatID := chat.ID
	agentReply := &AgentReply{
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
		CompressionEnabled:        chat.CompressionEnabled,
		HistoryKeepLastN:          a.historyKeepLastN,
		SummarizedMessageCount:    chat.SummarizedThrough,
		RawMessageCount:           len(chat.Messages) - chat.SummarizedThrough,
	}
	a.mu.Unlock()

	if event := a.compressHistoryIfDue(ctx, chatID); event != nil {
		agentReply.NewCompressionEvent = event
		a.mu.Lock()
		if chat, ok := a.chats[chatID]; ok {
			agentReply.SummarizedMessageCount = chat.SummarizedThrough
			agentReply.RawMessageCount = len(chat.Messages) - chat.SummarizedThrough
			agentReply.CumulativeTotalTokens = chat.CumulativeTotalTokens
			agentReply.CumulativeCostUsd = chat.CumulativeCostUsd
		}
		a.mu.Unlock()
	}

	return agentReply, nil
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
	if usage != nil {
		chat.CumulativeTotalTokens += usage.TotalTokens
		if usage.CostUsd != nil {
			if chat.CumulativeCostUsd == nil {
				cost := *usage.CostUsd
				chat.CumulativeCostUsd = &cost
			} else {
				*chat.CumulativeCostUsd += *usage.CostUsd
			}
		}
	}
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

// compressHistoryIfDue is the automatic trigger, called after every turn:
// once the raw (not-yet-summarized) tail has grown past 2*historyKeepLastN
// messages, it folds everything except the last historyKeepLastN back into
// the rolling Summary. This keeps the raw tail actually sent to the model
// between N and 2N messages at all times, instead of growing without bound.
// A failure here (e.g. the summarization call itself hits the upstream
// context/rate limits) is logged and swallowed — it must never fail the
// user's own turn, which has already completed by the time this runs; the
// next trigger will simply try again with a larger segment.
func (a *Agent) compressHistoryIfDue(ctx context.Context, chatID string) *CompressionEvent {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok || !chat.CompressionEnabled || a.historyKeepLastN <= 0 {
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
// ignoring the 2N auto-trigger threshold and chat.CompressionEnabled (a
// manual request overrides the auto toggle) — it only requires that at
// least one message beyond the last historyKeepLastN exists to fold in.
// Returns false, nil (not an error) when there's nothing to compress yet.
func (a *Agent) ForceCompress(ctx context.Context, chatID string) (bool, error) {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok {
		a.mu.Unlock()
		return false, ErrChatNotFound
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

// SetCompressionEnabled flips a single chat's own compression toggle without
// touching its already-accumulated Summary/SummarizedThrough — turning it
// off simply stops folding further messages in (PostMessage falls back to
// resending the full raw history), turning it back on resumes from wherever
// it left off.
func (a *Agent) SetCompressionEnabled(chatID string, enabled bool) (*Chat, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}
	chat.CompressionEnabled = enabled
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after compression toggle: %v", chat.ID, err)
	}

	copied := *chat
	copied.Messages = append([]AgentMessage(nil), chat.Messages...)
	return &copied, nil
}

// tokenUsageFrom converts the LiteLLM gateway's usage block into this app's
// own TokenUsage type, or nil if the upstream provider didn't report one.
func tokenUsageFrom(u *chatCompletionUsage) *TokenUsage {
	if u == nil {
		return nil
	}
	return &TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CostUsd:          u.Cost,
	}
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
		// if it were the chat reply is worse than an error, so treat it as
		// a real failure and let the caller decide (e.g. a truncation
		// budget turns this into a graceful context-limit notice).
		if strings.HasPrefix(cleaned, "{") {
			return nil, fmt.Errorf("%w: model's JSON response was malformed or cut off: %v", ErrInvalidOutput, err)
		}
		// Otherwise this is a plain conversational follow-up (e.g. "sum
		// these subtask hours for me") where the model dropped the JSON
		// envelope entirely and just answered in prose. Treat that prose as
		// the reply instead of failing the turn — it's still a useful
		// answer, and no estimate update was implied anyway.
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

// chatTitleFrom derives a short chat title from the first user message, so
// the chat list has something more useful than "Новый чат" to show.
func chatTitleFrom(firstMessage string) string {
	title := strings.TrimSpace(firstMessage)
	const maxRunes = 48
	runes := []rune(title)
	if len(runes) <= maxRunes {
		return title
	}
	return string(runes[:maxRunes]) + "…"
}

// truncateForLog caps a string for safe inclusion in a log line, since a raw
// LLM response can be several KB long.
func truncateForLog(s string) string {
	const maxLen = 500
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func chatSummary(c *Chat) ChatSummary {
	return ChatSummary{ID: c.ID, Title: c.Title, CreatedAt: c.CreatedAt}
}

// ChatDetail is one chat's full state as returned to the frontend, including
// its running token/cost totals and the context limit they're measured
// against, so a page reload restores the token panel exactly as a new
// message would have left it.
type ChatDetail struct {
	ID                     string            `json:"id"`
	Title                  string            `json:"title"`
	CreatedAt              time.Time         `json:"created_at"`
	Messages               []AgentMessage    `json:"messages"`
	Estimate               *EstimateResponse `json:"estimate"`
	LastContextTokens      int               `json:"last_context_tokens"`
	CumulativeTotalTokens  int               `json:"cumulative_total_tokens"`
	CumulativeCostUsd      *float64          `json:"cumulative_cost_usd,omitempty"`
	ContextTokenLimit      int               `json:"context_token_limit"`
	CompressionEnabled     bool              `json:"compression_enabled"`
	HistoryKeepLastN       int               `json:"history_keep_last_n"`
	SummarizedMessageCount int               `json:"summarized_message_count"`
	RawMessageCount        int               `json:"raw_message_count"`
	CompressionEvents      []CompressionEvent `json:"compression_events"`
}

func chatDetail(c *Chat, contextTokenLimit, historyKeepLastN int) ChatDetail {
	events := c.CompressionEvents
	if events == nil {
		events = []CompressionEvent{}
	}
	return ChatDetail{
		ID:                     c.ID,
		Title:                  c.Title,
		CreatedAt:              c.CreatedAt,
		Messages:               c.Messages,
		Estimate:               c.Estimate,
		LastContextTokens:      c.LastContextTokens,
		CumulativeTotalTokens:  c.CumulativeTotalTokens,
		CumulativeCostUsd:      c.CumulativeCostUsd,
		ContextTokenLimit:      contextTokenLimit,
		CompressionEnabled:     c.CompressionEnabled,
		HistoryKeepLastN:       historyKeepLastN,
		SummarizedMessageCount: c.SummarizedThrough,
		RawMessageCount:        len(c.Messages) - c.SummarizedThrough,
		CompressionEvents:      events,
	}
}
