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
// TaskState is set only on the assistant message of a real turn (see
// PostMessage) — a durable stamp of what stage the task was AT as of that
// reply, so a reloaded chat can still render each message's stage without
// the frontend having to recompute history retroactively (which it can't:
// EstimateRevisions/TaskDone are current-only counters, not a log). It's
// nil for user messages and for messages from before this field existed.
type AgentMessage struct {
	Role          string      `json:"role"`
	Content       string      `json:"content"`
	CreatedAt     time.Time   `json:"created_at"`
	Usage         *TokenUsage `json:"usage,omitempty"`
	IsLabAnalysis bool        `json:"is_lab_analysis,omitempty"`
	TaskState     *TaskState  `json:"task_state,omitempty"`
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

	// ProjectID is non-empty when this chat belongs to a Project (see
	// project.go) — a lab chat and a project chat are mutually exclusive by
	// construction (newChatLocked's two ID params are never both set).
	ProjectID string `json:"project_id,omitempty"`

	// Task is day 11's working-memory layer: this one chat's own task data
	// (goal, agreed constraints, answers gathered so far), auto-updated
	// after every real turn regardless of ContextStrategy — unlike Facts,
	// which only exists for chats on the sticky_facts strategy. It lives and
	// dies with this chat; it is never shared with any other chat, project
	// or not.
	Task *TaskMemory `json:"task,omitempty"`

	// TaskDone and EstimateRevisions back day 13's task state machine (see
	// task_state.go). The stage itself is never stored — always recomputed
	// by computeTaskState from Messages/Estimate/these two fields — so only
	// the one genuinely manual bit (has the user accepted the current
	// estimate) and a revision counter (for the "step" label) need
	// persisting.
	TaskDone          bool `json:"task_done,omitempty"`
	EstimateRevisions int  `json:"estimate_revisions,omitempty"`
}

// TaskMemory is one chat's working memory: data about the specific task this
// conversation is estimating, distinct from both the raw dialog (Messages)
// and any project-level long-term memory (Project.KnownStack/Notes).
// Constraints is settled facts/boundaries the user has agreed for THIS task
// (e.g. "офлайн-режим не нужен") — deliberately facts, not open questions:
// a decision silently forgotten once the raw message that stated it slides
// out of the short-term window is a real risk (the model could contradict
// it later); a follow-up question never asked isn't — the model can just
// ask it again from the live conversation, so tracking "still unanswered"
// separately added little the raw window didn't already cover. See
// memory_task.go for how this is kept up to date.
type TaskMemory struct {
	Goal              string            `json:"goal"`
	Constraints       []string          `json:"constraints"`
	ClarifyingAnswers map[string]string `json:"clarifying_answers"`
}

// branchMessages/appendToBranch, branchEstimate/setBranchEstimate, and
// branchLastContextTokens/setBranchLastContextTokens are the branch-scoped
// primitives every per-turn read/write goes through, all keyed by an
// explicit branchID rather than the chat's current ActiveBranchID — a turn
// in progress (mid-LLM-call, lock released) must keep writing to the branch
// it started on even if SetActiveBranch changes which branch is "active"
// while it's still running; reading c.ActiveBranchID again at write time
// would silently redirect that turn's result onto whatever branch the user
// has since switched to. activeMessages/activeEstimate/activeLastContextTokens
// below are thin convenience wrappers for callers that genuinely want
// "whichever branch is active right now" (chatDetail, GetChat) rather than
// "the branch a specific turn belongs to" (PostMessage).
func (c *Chat) branchMessages(branchID string) []AgentMessage {
	if c.ContextStrategy == StrategyBranching {
		if b := c.Branches[branchID]; b != nil {
			return b.Messages
		}
		return nil
	}
	return c.Messages
}

func (c *Chat) appendToBranch(branchID string, msgs ...AgentMessage) {
	if c.ContextStrategy == StrategyBranching {
		if b := c.Branches[branchID]; b != nil {
			b.Messages = append(b.Messages, msgs...)
			return
		}
	}
	c.Messages = append(c.Messages, msgs...)
}

func (c *Chat) branchEstimate(branchID string) *EstimateResponse {
	if c.ContextStrategy == StrategyBranching {
		if b := c.Branches[branchID]; b != nil {
			return b.Estimate
		}
		return nil
	}
	return c.Estimate
}

func (c *Chat) setBranchEstimate(branchID string, e *EstimateResponse) {
	if c.ContextStrategy == StrategyBranching {
		if b := c.Branches[branchID]; b != nil {
			b.Estimate = e
			return
		}
	}
	c.Estimate = e
}

func (c *Chat) branchLastContextTokens(branchID string) int {
	if c.ContextStrategy == StrategyBranching {
		if b := c.Branches[branchID]; b != nil {
			return b.LastContextTokens
		}
		return 0
	}
	return c.LastContextTokens
}

func (c *Chat) setBranchLastContextTokens(branchID string, n int) {
	if c.ContextStrategy == StrategyBranching {
		if b := c.Branches[branchID]; b != nil {
			b.LastContextTokens = n
			return
		}
	}
	c.LastContextTokens = n
}

// setLastMessageTaskState stamps the most recently appended message (the
// assistant reply of the turn that just ran) with the chat's stage as of
// right after that turn's own mutations (estimate/done/revisions) — must be
// called after those, and after appendToBranch, or it would stamp either
// the wrong message or the pre-turn stage.
func (c *Chat) setLastMessageTaskState(branchID string, state TaskState) {
	if c.ContextStrategy == StrategyBranching {
		if b := c.Branches[branchID]; b != nil && len(b.Messages) > 0 {
			b.Messages[len(b.Messages)-1].TaskState = &state
		}
		return
	}
	if len(c.Messages) > 0 {
		c.Messages[len(c.Messages)-1].TaskState = &state
	}
}

func (c *Chat) activeMessages() []AgentMessage    { return c.branchMessages(c.ActiveBranchID) }
func (c *Chat) activeEstimate() *EstimateResponse { return c.branchEstimate(c.ActiveBranchID) }
func (c *Chat) activeLastContextTokens() int      { return c.branchLastContextTokens(c.ActiveBranchID) }

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
// ContextStrategy/IsLabCoordinator let the sidebar render a lab's strategy
// badges and highlight its coordinator without fetching every chat's full
// detail.
type ChatSummary struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	CreatedAt        time.Time       `json:"created_at"`
	LabID            string          `json:"lab_id,omitempty"`
	ProjectID        string          `json:"project_id,omitempty"`
	ContextStrategy  ContextStrategy `json:"context_strategy"`
	IsLabCoordinator bool            `json:"is_lab_coordinator,omitempty"`
	TaskState        TaskState       `json:"task_state"`
}

// Agent is the entity that owns every chat and lab, encapsulating
// conversation state and the logic to turn a chat message into an LLM
// request and back. Handlers only translate HTTP to and from Agent's
// methods; they never call the LLM client directly.
type Agent struct {
	client            *LiteLLMClient
	store             *ChatStore
	labStore          *LabStore
	projectStore      *ProjectStore
	profileStore      *ProfileStore
	contextTokenLimit int // 0 disables the pre-call overflow guard entirely

	historyKeepLastN       int             // 0 disables windowing/compression entirely
	contextStrategyDefault ContextStrategy // initial Chat.ContextStrategy for new chats

	mu       sync.Mutex
	chats    map[string]*Chat
	order    []string // chat IDs, oldest first, for stable listing order
	labs     map[string]*Lab
	projects map[string]*Project
	profile  *UserProfile               // day 12: single global profile, never nil after NewAgent
	fanOut   map[string][]FanOutStatus // labID -> its most recent coordinator fan-out, in-memory only
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
func NewAgent(client *LiteLLMClient, store *ChatStore, labStore *LabStore, projectStore *ProjectStore, profileStore *ProfileStore, contextTokenLimit, historyKeepLastN int, contextStrategyDefault ContextStrategy) *Agent {
	agent := &Agent{
		client:                 client,
		store:                  store,
		labStore:               labStore,
		projectStore:           projectStore,
		profileStore:           profileStore,
		contextTokenLimit:      contextTokenLimit,
		historyKeepLastN:       historyKeepLastN,
		contextStrategyDefault: contextStrategyDefault,
		chats:                  make(map[string]*Chat),
		labs:                   make(map[string]*Lab),
		projects:               make(map[string]*Project),
		profile:                &UserProfile{Constraints: []string{}},
		fanOut:                 make(map[string][]FanOutStatus),
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
		// A chat persisted before Constraints existed (it was called
		// OpenQuestions) — or from any other schema change to TaskMemory —
		// loads with Task.Constraints as Go's nil zero value, since the old
		// key doesn't match this field's json tag. Normalized here, once, on
		// load, rather than leaving it nil until the next turn happens to
		// call updateTaskMemory's own normalization: the same "null where
		// the frontend expects an array" hazard fixed in copyChat/
		// updateTaskMemory, guarded here too since this path bypasses both.
		if chat.Task != nil {
			if chat.Task.Constraints == nil {
				chat.Task.Constraints = []string{}
			}
			if chat.Task.ClarifyingAnswers == nil {
				chat.Task.ClarifyingAnswers = map[string]string{}
			}
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

	projects, err := projectStore.LoadAll()
	if err != nil {
		log.Printf("agent: failed to load persisted projects: %v", err)
		return agent
	}
	for _, project := range projects {
		agent.projects[project.ID] = project
	}
	log.Printf("agent: restored %d project(s)", len(projects))

	if profile, err := profileStore.Load(); err != nil {
		log.Printf("agent: failed to load persisted profile, starting empty: %v", err)
	} else {
		agent.profile = profile
		log.Printf("agent: restored profile (name=%q)", profile.Name)
	}

	return agent
}

func newChatID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// newChatLocked creates and persists a chat with an explicit title/strategy/
// lab/project membership. Callers must hold a.mu. Shared by CreateChat (a
// plain new chat, agent-default strategy, optionally in a project, never in
// a lab) and CreateLab (one call per strategy chat in the lab, never in a
// project — labID and projectID are never both non-empty).
func (a *Agent) newChatLocked(title string, strategy ContextStrategy, labID, projectID string) *Chat {
	chat := &Chat{
		ID:              newChatID(),
		Title:           title,
		CreatedAt:       time.Now(),
		Messages:        []AgentMessage{},
		ContextStrategy: strategy,
		LabID:           labID,
		ProjectID:       projectID,
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
// strategy, optionally scoped to an existing project (empty projectID means
// no project), and returns its summary.
func (a *Agent) CreateChat(projectID string) (ChatSummary, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if projectID != "" {
		if _, ok := a.projects[projectID]; !ok {
			return ChatSummary{}, ErrProjectNotFound
		}
	}

	chat := a.newChatLocked("Новый чат", a.contextStrategyDefault, "", projectID)
	return chatSummary(chat, a.labs), nil
}

// ListChats returns every chat's summary, oldest first.
func (a *Agent) ListChats() []ChatSummary {
	a.mu.Lock()
	defer a.mu.Unlock()

	summaries := make([]ChatSummary, 0, len(a.order))
	for _, id := range a.order {
		summaries = append(summaries, chatSummary(a.chats[id], a.labs))
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
	return chatSummary(chat, a.labs), nil
}

// copyChat returns a copy of chat safe to hand to a caller outside the lock:
// a shallow copy with Messages replaced by the active strategy's own message
// slice (see Chat.activeMessages), deep-copied so the caller can't mutate
// agent state through it. Built with make+copy, not append(nil, ...): for a
// brand-new chat with zero messages, append(([]AgentMessage)(nil)) with no
// elements to add returns nil, not an allocated empty slice — that
// serialized as a JSON "messages": null, which crashed the frontend's
// [...prev.messages, ...] optimistic-append on the first message of any chat
// fetched (not just newly created) with none yet.
func copyChat(c *Chat) *Chat {
	copied := *c
	active := c.activeMessages()
	copied.Messages = make([]AgentMessage, len(active))
	copy(copied.Messages, active)
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
	FanOut                    []FanOutStatus    `json:"fan_out,omitempty"`
	ProjectID                 string            `json:"project_id,omitempty"`
	Project                   *Project          `json:"project,omitempty"`
	Task                      *TaskMemory       `json:"task,omitempty"`
	Profile                   *UserProfile      `json:"profile,omitempty"`
	TaskState                 TaskState         `json:"task_state"`
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

// chatSummary takes the Agent's live labs map (never re-locks — every caller
// already holds a.mu) so it can tell whether c is its lab's coordinator.
func chatSummary(c *Chat, labs map[string]*Lab) ChatSummary {
	isCoordinator := false
	if c.LabID != "" {
		if lab, ok := labs[c.LabID]; ok {
			isCoordinator = lab.CoordinatorChatID == c.ID
		}
	}
	return ChatSummary{
		ID:               c.ID,
		Title:            c.Title,
		CreatedAt:        c.CreatedAt,
		LabID:            c.LabID,
		ProjectID:        c.ProjectID,
		ContextStrategy:  c.ContextStrategy,
		IsLabCoordinator: isCoordinator,
		TaskState:        computeTaskState(c),
	}
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
	FanOut                 []FanOutStatus     `json:"fan_out,omitempty"`
	ProjectID              string             `json:"project_id,omitempty"`
	// Project is always populated from live agent state when the chat
	// belongs to one (see chatDetailWithLab in handler_agent.go), not only
	// when this request happened to change it — same "always current, never
	// stale" discipline every other strategy field here already follows.
	Project *Project    `json:"project,omitempty"`
	Task    *TaskMemory `json:"task,omitempty"`
	// Profile is the single global profile (day 12), always populated
	// regardless of this chat's project/lab — see chatDetailWithLab.
	Profile *UserProfile `json:"profile,omitempty"`
	// TaskState is day 13's task state machine — always populated, unlike
	// Task/Profile which are nil until something exists to report.
	TaskState TaskState `json:"task_state"`
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
		Estimate:               c.activeEstimate(),
		LastContextTokens:      c.activeLastContextTokens(),
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
		ProjectID:              c.ProjectID,
		Task:                   c.Task,
		TaskState:              computeTaskState(c),
	}
}
