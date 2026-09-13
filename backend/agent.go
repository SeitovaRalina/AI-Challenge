package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
empty array rather than artificially split into filler pieces. Every "reply"
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
// "user" or "assistant"; the system prompt is never exposed.
type AgentMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat is one independent conversation the Agent holds in memory: its own
// message history and the latest estimate produced within it, isolated from
// every other chat.
type Chat struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	CreatedAt time.Time         `json:"created_at"`
	Messages  []AgentMessage    `json:"messages"`
	Estimate  *EstimateResponse `json:"estimate"`
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
	client *LiteLLMClient
	store  *ChatStore

	mu    sync.Mutex
	chats map[string]*Chat
	order []string // chat IDs, oldest first, for stable listing order
}

// NewAgent restores every chat store persisted so a restart continues each
// conversation exactly where it left off; a store read failure is logged and
// treated as an empty history rather than aborting startup.
func NewAgent(client *LiteLLMClient, store *ChatStore) *Agent {
	agent := &Agent{
		client: client,
		store:  store,
		chats:  make(map[string]*Chat),
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
// produces one).
type AgentReply struct {
	Reply    string            `json:"reply"`
	Estimate *EstimateResponse `json:"estimate"`
	Title    string            `json:"title"`
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
	a.mu.Unlock()

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

	content, err := a.client.chatComplete(ctx, messages, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}

	turn, err := parseAgentTurn(content)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	chat.Messages = append(chat.Messages,
		AgentMessage{Role: "user", Content: userMessage},
		AgentMessage{Role: "assistant", Content: turn.Reply},
	)
	if turn.Estimate != nil {
		chat.Estimate = turn.Estimate
	}
	if chat.Title == "Новый чат" {
		chat.Title = chatTitleFrom(userMessage)
	}

	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}

	return &AgentReply{
		Reply:    turn.Reply,
		Estimate: chat.Estimate,
		Title:    chat.Title,
	}, nil
}

// parseAgentTurn strips optional code fences and unmarshals the model's
// {"reply", "estimate"} envelope, validating the estimate against the same
// schema the day-1 estimate endpoint enforces whenever one is present.
func parseAgentTurn(raw string) (*agentTurn, error) {
	cleaned := stripCodeFences(raw)

	var turn agentTurn
	if err := json.Unmarshal([]byte(cleaned), &turn); err != nil {
		return nil, fmt.Errorf("%w: model did not return valid JSON: %v", ErrInvalidOutput, err)
	}
	if strings.TrimSpace(turn.Reply) == "" {
		return nil, fmt.Errorf("%w: reply must not be empty", ErrInvalidOutput)
	}
	if turn.Estimate != nil {
		if err := turn.Estimate.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
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

func chatSummary(c *Chat) ChatSummary {
	return ChatSummary{ID: c.ID, Title: c.Title, CreatedAt: c.CreatedAt}
}

// ChatDetail is one chat's full state as returned to the frontend.
type ChatDetail struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	CreatedAt time.Time         `json:"created_at"`
	Messages  []AgentMessage    `json:"messages"`
	Estimate  *EstimateResponse `json:"estimate"`
}

func chatDetail(c *Chat) ChatDetail {
	return ChatDetail{
		ID:        c.ID,
		Title:     c.Title,
		CreatedAt: c.CreatedAt,
		Messages:  c.Messages,
		Estimate:  c.Estimate,
	}
}
