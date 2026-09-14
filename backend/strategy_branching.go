package main

import (
	"fmt"
	"log"
	"sort"
	"time"
)

// Checkpoint marks a point in one branch's history a new branch can later be
// forked from. Recording it server-side (rather than leaving it purely
// client-side) means it survives a reload and can be listed in a chat's
// history.
type Checkpoint struct {
	Index     int       `json:"index"`     // message count of BranchID at the time it was created
	BranchID  string    `json:"branch_id"` // which branch this checkpoint was taken in
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// Branch is one independent continuation of a chat's conversation, forked
// from another branch at ForkIndex. Its Messages start as a copy of the
// parent's messages up to that point and grow independently afterward —
// branches intentionally duplicate that shared prefix rather than share it,
// since at this app's scale (a handful of demo branches) that's far simpler
// than a shared-prefix tree, at the cost of some redundant storage.
type Branch struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	ParentID  string         `json:"parent_id,omitempty"`
	ForkIndex int            `json:"fork_index"`
	Messages  []AgentMessage `json:"messages"`
	CreatedAt time.Time      `json:"created_at"`
}

// BranchSummary is a branch's identity and size, without its own message
// history — ChatDetail/AgentReply only ever need the active branch's full
// messages (already exposed as Messages/activeMessages); every other branch
// is just listed so the UI can render tabs and switch between them.
type BranchSummary struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	ParentID     string    `json:"parent_id,omitempty"`
	ForkIndex    int       `json:"fork_index"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
}

func branchSummaries(c *Chat) []BranchSummary {
	if len(c.Branches) == 0 {
		return []BranchSummary{}
	}
	summaries := make([]BranchSummary, 0, len(c.Branches))
	for _, b := range c.Branches {
		summaries = append(summaries, BranchSummary{
			ID:           b.ID,
			Label:        b.Label,
			ParentID:     b.ParentID,
			ForkIndex:    b.ForkIndex,
			MessageCount: len(b.Messages),
			CreatedAt:    b.CreatedAt,
		})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].CreatedAt.Before(summaries[j].CreatedAt) })
	return summaries
}

// ensureBranchState makes sure chat has a "main" branch (seeded from
// whatever chat.Messages held so far) and an ActiveBranchID, the first time
// a chat's strategy becomes branching. Calling it again on an already-set-up
// branching chat is a no-op.
func ensureBranchState(chat *Chat) {
	if chat.Branches == nil {
		chat.Branches = map[string]*Branch{}
	}
	if _, ok := chat.Branches["main"]; !ok {
		chat.Branches["main"] = &Branch{
			ID:        "main",
			Label:     "Основная",
			Messages:  append([]AgentMessage(nil), chat.Messages...),
			CreatedAt: chat.CreatedAt,
		}
	}
	if chat.ActiveBranchID == "" {
		chat.ActiveBranchID = "main"
	}
}

// CreateCheckpoint records a checkpoint at the active branch's current
// length, so a branch can be forked from this exact point later — including
// after further messages have been sent in the meantime.
func (a *Agent) CreateCheckpoint(chatID, label string) (*Chat, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}
	if chat.ContextStrategy != StrategyBranching {
		return nil, ErrWrongStrategy
	}
	ensureBranchState(chat)
	active := chat.Branches[chat.ActiveBranchID]

	chat.Checkpoints = append(chat.Checkpoints, Checkpoint{
		Index:     len(active.Messages),
		BranchID:  chat.ActiveBranchID,
		Label:     label,
		CreatedAt: time.Now(),
	})
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after checkpoint: %v", chat.ID, err)
	}
	log.Printf("agent: chat %s: checkpoint %q at message %d of branch %s", chatID, label, len(active.Messages), chat.ActiveBranchID)
	return copyChat(chat), nil
}

// CreateBranch forks a new branch from fromBranchID's first checkpointIndex
// messages. Two branches created from the same checkpoint (same
// fromBranchID/checkpointIndex, different labels) is exactly how the day-10
// assignment's "create 2 branches from one point" is done.
func (a *Agent) CreateBranch(chatID string, checkpointIndex int, fromBranchID, label string) (*Chat, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}
	if chat.ContextStrategy != StrategyBranching {
		return nil, ErrWrongStrategy
	}
	ensureBranchState(chat)

	source, ok := chat.Branches[fromBranchID]
	if !ok {
		return nil, ErrBranchNotFound
	}
	if checkpointIndex < 0 || checkpointIndex > len(source.Messages) {
		return nil, fmt.Errorf("agent: checkpoint index %d out of range for branch %s (%d message(s))", checkpointIndex, fromBranchID, len(source.Messages))
	}

	branch := &Branch{
		ID:        newChatID(),
		Label:     label,
		ParentID:  fromBranchID,
		ForkIndex: checkpointIndex,
		Messages:  append([]AgentMessage(nil), source.Messages[:checkpointIndex]...),
		CreatedAt: time.Now(),
	}
	chat.Branches[branch.ID] = branch
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after branch creation: %v", chat.ID, err)
	}
	log.Printf("agent: chat %s: forked branch %q (%s) from %s at message %d", chatID, label, branch.ID, fromBranchID, checkpointIndex)
	return copyChat(chat), nil
}

// SetActiveBranch switches which branch PostMessage/GetChat operate on.
func (a *Agent) SetActiveBranch(chatID, branchID string) (*Chat, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}
	if chat.ContextStrategy != StrategyBranching {
		return nil, ErrWrongStrategy
	}
	if _, ok := chat.Branches[branchID]; !ok {
		return nil, ErrBranchNotFound
	}
	chat.ActiveBranchID = branchID
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after branch switch: %v", chat.ID, err)
	}
	return copyChat(chat), nil
}
