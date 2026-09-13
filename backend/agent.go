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
// every other chat. LastPromptTokens is the prompt_tokens of the most recent
// LLM call — the full conversation-so-far as the model actually counted it —
// and is what PostMessage compares against the agent's context token limit
// before spending another call; CumulativeTotalTokens/CumulativeCostUsd are a
// running sum across every turn, for display only.
type Chat struct {
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	CreatedAt             time.Time         `json:"created_at"`
	Messages              []AgentMessage    `json:"messages"`
	Estimate              *EstimateResponse `json:"estimate"`
	LastPromptTokens      int               `json:"last_prompt_tokens"`
	CumulativeTotalTokens int               `json:"cumulative_total_tokens"`
	CumulativeCostUsd     *float64          `json:"cumulative_cost_usd,omitempty"`
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
func NewAgent(client *LiteLLMClient, store *ChatStore, contextTokenLimit int) *Agent {
	agent := &Agent{
		client:            client,
		store:             store,
		contextTokenLimit: contextTokenLimit,
		chats:             make(map[string]*Chat),
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
		ID:        newChatID(),
		Title:     "Новый чат",
		CreatedAt: time.Now(),
		Messages:  []AgentMessage{},
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
	Reply                      string            `json:"reply"`
	Estimate                   *EstimateResponse `json:"estimate"`
	Title                      string            `json:"title"`
	Usage                      *TokenUsage       `json:"usage"`
	UserMessageCreatedAt       time.Time         `json:"user_message_created_at"`
	AssistantMessageCreatedAt  time.Time         `json:"assistant_message_created_at"`
	LastPromptTokens           int               `json:"last_prompt_tokens"`
	CumulativeTotalTokens      int               `json:"cumulative_total_tokens"`
	CumulativeCostUsd          *float64          `json:"cumulative_cost_usd,omitempty"`
	ContextTokenLimit          int               `json:"context_token_limit"`
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
	lastPromptTokens := chat.LastPromptTokens
	a.mu.Unlock()

	log.Printf("agent: chat %s: turn %d, message length %d", chatID, len(history)/2+1, len(userMessage))

	userSentAt := time.Now()

	// Pre-call overflow guard: the previous turn's prompt_tokens is the
	// model's own count of the entire conversation as of that call, and this
	// turn's history is at most a couple of messages larger — close enough to
	// refuse a call that would certainly overflow without spending it. This
	// is also what makes the artificially low demo limit (CHAT_CONTEXT_TOKEN_LIMIT)
	// break the dialog deterministically instead of depending on ever
	// actually hitting the real model's context window.
	if a.contextTokenLimit > 0 && lastPromptTokens >= a.contextTokenLimit {
		log.Printf("agent: chat %s: context limit reached (%d/%d tokens), skipping LLM call",
			chatID, lastPromptTokens, a.contextTokenLimit)
		return a.finishOverflowTurn(chat, userMessage, userSentAt, lastPromptTokens)
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
	for _, m := range history {
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, chatMessage{Role: "user", Content: userMessage})

	callStart := time.Now()
	completion, err := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, 0, nil)
	if err != nil {
		// A real upstream context-length rejection is handled the same
		// gracefully-in-chat way as the pre-call guard above, instead of
		// surfacing as a 502 to the user.
		if errors.Is(err, ErrContextOverflow) {
			log.Printf("agent: chat %s: model rejected the request as over its context length: %v", chatID, err)
			return a.finishOverflowTurn(chat, userMessage, userSentAt, lastPromptTokens)
		}
		log.Printf("agent: chat %s: LLM call failed after %s: %v", chatID, time.Since(callStart).Round(time.Millisecond), err)
		return nil, err
	}
	log.Printf("agent: chat %s: LLM call took %s", chatID, time.Since(callStart).Round(time.Millisecond))

	turn, err := parseAgentTurn(completion.Choices[0].Message.Content)
	if err != nil {
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
	defer a.mu.Unlock()

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
		chat.LastPromptTokens = usage.PromptTokens
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

	return &AgentReply{
		Reply:                     turn.Reply,
		Estimate:                  chat.Estimate,
		Title:                     chat.Title,
		Usage:                     usage,
		UserMessageCreatedAt:      userSentAt,
		AssistantMessageCreatedAt: assistantSentAt,
		LastPromptTokens:          chat.LastPromptTokens,
		CumulativeTotalTokens:     chat.CumulativeTotalTokens,
		CumulativeCostUsd:         chat.CumulativeCostUsd,
		ContextTokenLimit:         a.contextTokenLimit,
	}, nil
}

// overflowReplyText is shown in the chat itself (not as an HTTP error) when
// the context token limit blocks a turn, so overflow reads as a handled
// product state instead of a crash.
func overflowReplyText(tokens, limit int) string {
	return fmt.Sprintf(
		"⚠️ Достигнут лимит контекста диалога: %d / %d токенов. "+
			"Продолжить этот диалог нельзя — начните новый чат.",
		tokens, limit,
	)
}

// finishOverflowTurn appends the user's message and a synthetic overflow
// reply to chat without ever calling the LLM, persists it, and returns the
// same AgentReply shape a normal turn would — so the frontend needs no
// special-casing for this path.
func (a *Agent) finishOverflowTurn(chat *Chat, userMessage string, userSentAt time.Time, lastPromptTokens int) (*AgentReply, error) {
	assistantSentAt := time.Now()
	reply := overflowReplyText(lastPromptTokens, a.contextTokenLimit)

	a.mu.Lock()
	defer a.mu.Unlock()

	chat.Messages = append(chat.Messages,
		AgentMessage{Role: "user", Content: userMessage, CreatedAt: userSentAt},
		AgentMessage{Role: "assistant", Content: reply, CreatedAt: assistantSentAt},
	)
	if chat.Title == "Новый чат" {
		chat.Title = chatTitleFrom(userMessage)
	}
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}

	return &AgentReply{
		Reply:                     reply,
		Estimate:                  chat.Estimate,
		Title:                     chat.Title,
		Usage:                     nil,
		UserMessageCreatedAt:      userSentAt,
		AssistantMessageCreatedAt: assistantSentAt,
		LastPromptTokens:          chat.LastPromptTokens,
		CumulativeTotalTokens:     chat.CumulativeTotalTokens,
		CumulativeCostUsd:         chat.CumulativeCostUsd,
		ContextTokenLimit:         a.contextTokenLimit,
	}, nil
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
		// For a plain conversational follow-up (e.g. "sum these subtask
		// hours for me") the model sometimes drops the JSON envelope
		// entirely and just answers in prose. Treat that prose as the
		// reply instead of failing the whole turn — it's still a useful
		// answer, and no estimate update was implied anyway.
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: model did not return valid JSON: %v", ErrInvalidOutput, err)
		}
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
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	CreatedAt             time.Time         `json:"created_at"`
	Messages              []AgentMessage    `json:"messages"`
	Estimate              *EstimateResponse `json:"estimate"`
	LastPromptTokens      int               `json:"last_prompt_tokens"`
	CumulativeTotalTokens int               `json:"cumulative_total_tokens"`
	CumulativeCostUsd     *float64          `json:"cumulative_cost_usd,omitempty"`
	ContextTokenLimit     int               `json:"context_token_limit"`
}

func chatDetail(c *Chat, contextTokenLimit int) ChatDetail {
	return ChatDetail{
		ID:                    c.ID,
		Title:                 c.Title,
		CreatedAt:             c.CreatedAt,
		Messages:              c.Messages,
		Estimate:              c.Estimate,
		LastPromptTokens:      c.LastPromptTokens,
		CumulativeTotalTokens: c.CumulativeTotalTokens,
		CumulativeCostUsd:     c.CumulativeCostUsd,
		ContextTokenLimit:     contextTokenLimit,
	}
}
