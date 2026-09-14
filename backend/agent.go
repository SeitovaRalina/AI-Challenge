package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// AgentMessage is one chat message as returned to the frontend. Role is
// "user" or "assistant"; the system prompt is never exposed. Usage holds the
// token accounting of the LLM call this message's turn produced — the same
// object on both the user and assistant message of one turn, since both were
// billed by that single call. It is nil for messages restored from before
// this field existed, and for the synthetic reply a context-overflow turn
// returns without ever calling the LLM. IsLabAnalysis marks the one message
// AnalyzeLab appends — the frontend renders it distinctly from a normal reply.
type AgentMessage struct {
	Role          string      `json:"role"`
	Content       string      `json:"content"`
	CreatedAt     time.Time   `json:"created_at"`
	Usage         *TokenUsage `json:"usage,omitempty"`
	IsLabAnalysis bool        `json:"is_lab_analysis,omitempty"`
}

// Chat is one independent conversation the Agent holds in memory: its own
// message history and the latest estimate produced within it, isolated from
// every other chat. LastContextTokens is prompt_tokens + completion_tokens of
// the most recent LLM call — the full conversation-so-far AND the reply it
// just produced, since that reply is appended to history and will itself be
// sent back as part of the prompt on the next turn. It's what PostMessage
// compares against the agent's context token limit before spending another
// call, and what the UI shows as "context used" — using prompt_tokens alone
// would under-count by exactly the size of the last reply. CumulativeTotalTokens
// /CumulativeCostUsd are a running sum across every turn (and every strategy
// side-call: summarization, facts extraction, lab analysis), for display only.
//
// ContextStrategy picks how PostMessage turns Messages (or, for branching,
// the active Branch's messages) into the prompt sent to the model — see
// context_strategy.go. Every strategy's own state lives on Chat so switching
// strategies mid-chat never loses what a previous strategy had built up:
// Summary/SummarizedThrough/CompressionEvents belong to rolling_summary,
// Facts to sticky_facts, Checkpoints/Branches/ActiveBranchID to branching.
// Per the same never-delete invariant Day 7 established, no strategy ever
// trims or deletes a message — they only control what's resent to the model.
type Chat struct {
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	CreatedAt             time.Time         `json:"created_at"`
	Messages              []AgentMessage    `json:"messages"`
	Estimate              *EstimateResponse `json:"estimate"`
	LastContextTokens     int               `json:"last_context_tokens"`
	CumulativeTotalTokens int               `json:"cumulative_total_tokens"`
	CumulativeCostUsd     *float64          `json:"cumulative_cost_usd,omitempty"`

	ContextStrategy ContextStrategy `json:"context_strategy"`

	// rolling_summary state (day 9).
	Summary           string             `json:"summary,omitempty"`
	SummarizedThrough int                `json:"summarized_through"`
	CompressionEvents []CompressionEvent `json:"compression_events"`

	// sticky_facts state.
	Facts map[string]string `json:"facts,omitempty"`

	// branching state.
	Checkpoints    []Checkpoint       `json:"checkpoints,omitempty"`
	Branches       map[string]*Branch `json:"branches,omitempty"`
	ActiveBranchID string             `json:"active_branch_id,omitempty"`

	// LabID is non-empty when this chat was created as one of a
	// ContextLab's strategy chats (see lab.go) — empty for a normal chat.
	LabID string `json:"lab_id,omitempty"`
}

// activeMessages returns the message slice PostMessage/chatDetail should
// read: the active Branch's messages under the branching strategy, or
// Messages for every other strategy. Centralizing this here means only this
// one method (and its write-side counterpart, appendActiveMessages) needs to
// know branching is special — everything else just calls it.
func (c *Chat) activeMessages() []AgentMessage {
	if c.ContextStrategy == StrategyBranching {
		if b := c.Branches[c.ActiveBranchID]; b != nil {
			return b.Messages
		}
		return nil
	}
	return c.Messages
}

// appendActiveMessages is activeMessages' write-side counterpart.
func (c *Chat) appendActiveMessages(msgs ...AgentMessage) {
	if c.ContextStrategy == StrategyBranching {
		if b := c.Branches[c.ActiveBranchID]; b != nil {
			b.Messages = append(b.Messages, msgs...)
			return
		}
	}
	c.Messages = append(c.Messages, msgs...)
}

// addUsage folds one LLM call's usage into the chat's running totals — every
// strategy side-call (summarization, facts extraction, lab analysis) bills
// the same chat its turn belongs to, exactly like the main turn call does.
func (c *Chat) addUsage(usage *TokenUsage) {
	if usage == nil {
		return
	}
	c.CumulativeTotalTokens += usage.TotalTokens
	if usage.CostUsd == nil {
		return
	}
	if c.CumulativeCostUsd == nil {
		cost := *usage.CostUsd
		c.CumulativeCostUsd = &cost
		return
	}
	*c.CumulativeCostUsd += *usage.CostUsd
}

// ChatSummary is a chat's identity without its message history, for listing.
type ChatSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	LabID     string    `json:"lab_id,omitempty"`
}

// Agent is the entity that owns every chat and lab, encapsulating
// conversation state and the logic to turn a chat message into an LLM
// request and back. Handlers only translate HTTP to and from Agent's
// methods; they never call the LLM client directly.
type Agent struct {
	client            *LiteLLMClient
	store             *ChatStore
	labStore          *LabStore
	contextTokenLimit int // 0 disables the pre-call overflow guard entirely

	historyKeepLastN       int             // 0 disables windowing/compression entirely
	contextStrategyDefault ContextStrategy // initial Chat.ContextStrategy for new chats

	mu    sync.Mutex
	chats map[string]*Chat
	order []string // chat IDs, oldest first, for stable listing order
	labs  map[string]*Lab
}

// NewAgent restores every chat and lab persisted so a restart continues each
// conversation exactly where it left off. A legacy chat persisted before
// ContextStrategy existed loads with it empty; it's normalized to
// sliding_window (the cheapest strategy — no extra LLM calls) rather than
// left blank. contextTokenLimit is the token budget PostMessage guards
// against before every LLM call; pass 0 to disable the guard.
// historyKeepLastN is how many of the most recent messages sliding_window/
// sticky_facts resend, and how many rolling_summary keeps raw before folding;
// pass 0 to disable windowing/compression entirely, for every chat.
// contextStrategyDefault seeds new chats' ContextStrategy.
func NewAgent(client *LiteLLMClient, store *ChatStore, labStore *LabStore, contextTokenLimit, historyKeepLastN int, contextStrategyDefault ContextStrategy) *Agent {
	agent := &Agent{
		client:                 client,
		store:                  store,
		labStore:               labStore,
		contextTokenLimit:      contextTokenLimit,
		historyKeepLastN:       historyKeepLastN,
		contextStrategyDefault: contextStrategyDefault,
		chats:                  make(map[string]*Chat),
		labs:                   make(map[string]*Lab),
	}

	chats, err := store.LoadAll()
	if err != nil {
		log.Printf("agent: failed to load persisted chats: %v", err)
		return agent
	}
	for _, chat := range chats {
		if chat.ContextStrategy == "" {
			chat.ContextStrategy = StrategySlidingWindow
		}
		if chat.ContextStrategy == StrategyBranching {
			ensureBranchState(chat)
		}
		agent.chats[chat.ID] = chat
		agent.order = append(agent.order, chat.ID)
	}
	log.Printf("agent: restored %d chat(s) from %s", len(chats), store.dir)

	labs, err := labStore.LoadAll()
	if err != nil {
		log.Printf("agent: failed to load persisted labs: %v", err)
		return agent
	}
	for _, lab := range labs {
		agent.labs[lab.ID] = lab
	}
	log.Printf("agent: restored %d lab(s)", len(labs))

	return agent
}

func newChatID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// newChatLocked creates and persists a chat with an explicit title/strategy/
// lab membership. Callers must hold a.mu. Shared by CreateChat (a plain new
// chat, agent-default strategy, no lab) and CreateLab (one call per strategy
// chat in the lab).
func (a *Agent) newChatLocked(title string, strategy ContextStrategy, labID string) *Chat {
	chat := &Chat{
		ID:              newChatID(),
		Title:           title,
		CreatedAt:       time.Now(),
		Messages:        []AgentMessage{},
		ContextStrategy: strategy,
		LabID:           labID,
	}
	if strategy == StrategyBranching {
		ensureBranchState(chat)
	}
	a.chats[chat.ID] = chat
	a.order = append(a.order, chat.ID)

	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist new chat %s: %v", chat.ID, err)
	}
	log.Printf("agent: created chat %s (strategy=%s)", chat.ID, strategy)
	return chat
}

// CreateChat starts a new, empty conversation using the agent's default
// strategy and returns its summary.
func (a *Agent) CreateChat() ChatSummary {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat := a.newChatLocked("Новый чат", a.contextStrategyDefault, "")
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

var (
	ErrChatNotFound   = fmt.Errorf("agent: chat not found")
	ErrWrongStrategy  = fmt.Errorf("agent: operation not available for this chat's context strategy")
	ErrBranchNotFound = fmt.Errorf("agent: branch not found")
)

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

// copyChat returns a copy of chat safe to hand to a caller outside the lock:
// a shallow copy with Messages replaced by the active strategy's own message
// slice (see Chat.activeMessages), deep-copied so the caller can't mutate
// agent state through it.
func copyChat(c *Chat) *Chat {
	copied := *c
	copied.Messages = append([]AgentMessage(nil), c.activeMessages()...)
	return &copied
}

// GetChat returns one chat's full state (history and current estimate).
func (a *Agent) GetChat(chatID string) (*Chat, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}
	return copyChat(chat), nil
}

// AgentReply is what one chat turn returns to the caller: the assistant's
// visible reply plus the chat's current estimate (nil until the first turn
// produces one), the token accounting of this turn's LLM call (nil if the
// context-overflow guard skipped it), the chat's running token/cost totals,
// and every strategy's own current state — only the fields relevant to the
// chat's active ContextStrategy are ever non-empty, but all are always
// populated from live chat state rather than left stale, the same lesson
// Day 8/9 already learned the hard way.
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
	ContextStrategy           ContextStrategy   `json:"context_strategy"`
	HistoryKeepLastN          int               `json:"history_keep_last_n"`
	SummarizedMessageCount    int               `json:"summarized_message_count"`
	RawMessageCount           int               `json:"raw_message_count"`
	NewCompressionEvent       *CompressionEvent `json:"new_compression_event,omitempty"`
	Facts                     map[string]string `json:"facts,omitempty"`
	Branches                  []BranchSummary   `json:"branches,omitempty"`
	ActiveBranchID            string            `json:"active_branch_id,omitempty"`
	LabID                     string            `json:"lab_id,omitempty"`
	IsLabCoordinator          bool              `json:"is_lab_coordinator,omitempty"`
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
	return ChatSummary{ID: c.ID, Title: c.Title, CreatedAt: c.CreatedAt, LabID: c.LabID}
}

// ChatDetail is one chat's full state as returned to the frontend, including
// its running token/cost totals and the context limit they're measured
// against, so a page reload restores the token panel exactly as a new
// message would have left it. Messages is the active strategy's own message
// slice (see Chat.activeMessages) — for branching, that's the active
// branch's messages, not the whole chat's.
type ChatDetail struct {
	ID                     string             `json:"id"`
	Title                  string             `json:"title"`
	CreatedAt              time.Time          `json:"created_at"`
	Messages               []AgentMessage     `json:"messages"`
	Estimate               *EstimateResponse  `json:"estimate"`
	LastContextTokens      int                `json:"last_context_tokens"`
	CumulativeTotalTokens  int                `json:"cumulative_total_tokens"`
	CumulativeCostUsd      *float64           `json:"cumulative_cost_usd,omitempty"`
	ContextTokenLimit      int                `json:"context_token_limit"`
	ContextStrategy        ContextStrategy    `json:"context_strategy"`
	HistoryKeepLastN       int                `json:"history_keep_last_n"`
	SummarizedMessageCount int                `json:"summarized_message_count"`
	RawMessageCount        int                `json:"raw_message_count"`
	CompressionEvents      []CompressionEvent `json:"compression_events"`
	Facts                  map[string]string  `json:"facts,omitempty"`
	Checkpoints            []Checkpoint       `json:"checkpoints,omitempty"`
	Branches               []BranchSummary    `json:"branches,omitempty"`
	ActiveBranchID         string             `json:"active_branch_id,omitempty"`
	LabID                  string             `json:"lab_id,omitempty"`
	IsLabCoordinator       bool               `json:"is_lab_coordinator,omitempty"`
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
		Messages:               c.activeMessages(),
		Estimate:               c.Estimate,
		LastContextTokens:      c.LastContextTokens,
		CumulativeTotalTokens:  c.CumulativeTotalTokens,
		CumulativeCostUsd:      c.CumulativeCostUsd,
		ContextTokenLimit:      contextTokenLimit,
		ContextStrategy:        c.ContextStrategy,
		HistoryKeepLastN:       historyKeepLastN,
		SummarizedMessageCount: c.SummarizedThrough,
		RawMessageCount:        len(c.Messages) - c.SummarizedThrough,
		CompressionEvents:      events,
		Facts:                  c.Facts,
		Checkpoints:            c.Checkpoints,
		Branches:               branchSummaries(c),
		ActiveBranchID:         c.ActiveBranchID,
		LabID:                  c.LabID,
	}
}
