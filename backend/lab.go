package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Lab groups the chats created by "+ Лаборатория": one chat per strategy in
// labStrategies, all fed the same messages, so the day-10 assignment's own
// testing method ("run the same scenario under each strategy, compare") is
// one click instead of manually retyping every message into several chats.
type Lab struct {
	ID                string    `json:"id"`
	Label             string    `json:"label"`
	ChatIDs           []string  `json:"chat_ids"`
	CoordinatorChatID string    `json:"coordinator_chat_id"`
	CreatedAt         time.Time `json:"created_at"`
}

// labStrategies is fixed on purpose: exactly the 3 strategies day 10 asks
// for, in this order — index 0 becomes the lab's coordinator chat, the one
// the user actually types into. rolling_summary (day 9) is deliberately left
// out of labs; it's still available as a normal chat's own strategy.
var labStrategies = []ContextStrategy{StrategySlidingWindow, StrategyStickyFacts, StrategyBranching}

func strategyLabel(s ContextStrategy) string {
	switch s {
	case StrategySlidingWindow:
		return "sliding window"
	case StrategyStickyFacts:
		return "sticky facts"
	case StrategyBranching:
		return "branching"
	case StrategyRollingSummary:
		return "rolling summary"
	default:
		return string(s)
	}
}

var ErrLabNotFound = fmt.Errorf("agent: lab not found")

// IsLabCoordinator reports whether chatID is labID's coordinator chat — the
// one the user actually types into, and the only one PostMessageWithFanOut
// fans a message out from. Safe to call with an empty labID (returns false).
func (a *Agent) IsLabCoordinator(labID, chatID string) bool {
	if labID == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	lab, ok := a.labs[labID]
	return ok && lab.CoordinatorChatID == chatID
}

// CreateLab creates one chat per strategy in labStrategies, all tagged with
// label in their title, groups them under a new Lab, and returns both.
func (a *Agent) CreateLab(label string) (*Lab, []ChatSummary, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, nil, fmt.Errorf("agent: lab label must not be empty")
	}

	lab := &Lab{ID: newChatID(), Label: label, CreatedAt: time.Now()}

	a.mu.Lock()
	summaries := make([]ChatSummary, 0, len(labStrategies))
	for i, strategy := range labStrategies {
		title := fmt.Sprintf("%s [%s]", label, strategyLabel(strategy))
		chat := a.newChatLocked(title, strategy, lab.ID)
		lab.ChatIDs = append(lab.ChatIDs, chat.ID)
		if i == 0 {
			lab.CoordinatorChatID = chat.ID
		}
		summaries = append(summaries, chatSummary(chat))
	}
	a.labs[lab.ID] = lab
	a.mu.Unlock()

	if err := a.labStore.Save(lab); err != nil {
		log.Printf("agent: failed to persist lab %s: %v", lab.ID, err)
	}
	log.Printf("agent: created lab %s %q with %d chat(s), coordinator %s", lab.ID, label, len(lab.ChatIDs), lab.CoordinatorChatID)

	return lab, summaries, nil
}

// PostMessageWithFanOut sends message through chatID's own PostMessage as
// usual, then — only if chatID is a lab's coordinator chat — fires the same
// message into every other chat of that lab in the background, each under
// its own strategy. The caller's response only ever waits on the
// coordinator's own reply; siblings run with a fresh background context
// (the request's own ctx is cancelled the moment the HTTP handler returns)
// and a failure in one is logged, never surfaced — a stalled sibling must
// never block or fail the chat the user is actually looking at.
func (a *Agent) PostMessageWithFanOut(ctx context.Context, chatID, userMessage string) (*AgentReply, error) {
	reply, err := a.PostMessage(ctx, chatID, userMessage)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	var siblings []string
	if chat, ok := a.chats[chatID]; ok && chat.LabID != "" {
		if lab, ok := a.labs[chat.LabID]; ok && lab.CoordinatorChatID == chatID {
			for _, id := range lab.ChatIDs {
				if id != chatID {
					siblings = append(siblings, id)
				}
			}
		}
	}
	a.mu.Unlock()

	for _, siblingID := range siblings {
		go func(id string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if _, err := a.PostMessage(bgCtx, id, userMessage); err != nil {
				log.Printf("agent: lab fan-out to chat %s failed: %v", id, err)
			}
		}(siblingID)
	}

	return reply, nil
}

// labAnalysisSystemPrompt asks the model to compare each strategy's
// transcript along exactly the 4 axes the day-10 assignment names, so the
// resulting message doubles as the assignment's own required comparison.
const labAnalysisSystemPrompt = `You are comparing how the SAME conversation played out under different context-management strategies in a software-task-estimation assistant.

You will see one full transcript per strategy (and, where relevant, its current facts or branch state) plus its token/cost totals. Compare them along exactly these four points, each its own short labeled paragraph, in Russian:

1. Качество ответов
2. Стабильность (не теряет ли важные детали из начала диалога)
3. Расход токенов
4. Удобство для пользователя

Be concrete — reference actual differences you see in the transcripts and the numbers given, not generic statements that could apply to any comparison. Output plain text only: no JSON, no markdown formatting, no headers beyond the four numbered points themselves.`

// AnalyzeLab gathers every chat in labID's full transcript and current
// strategy state, asks the LLM for one comparison along the assignment's own
// 4 axes, and appends that analysis as a real assistant message (flagged
// IsLabAnalysis so the frontend can style it distinctly) into the lab's
// coordinator chat — so the comparison lives in the conversation itself,
// not only in a PR description.
func (a *Agent) AnalyzeLab(ctx context.Context, labID string) (*AgentReply, error) {
	a.mu.Lock()
	lab, ok := a.labs[labID]
	if !ok {
		a.mu.Unlock()
		return nil, ErrLabNotFound
	}
	chats := make([]*Chat, 0, len(lab.ChatIDs))
	for _, id := range lab.ChatIDs {
		if c, ok := a.chats[id]; ok {
			chats = append(chats, copyChat(c))
		}
	}
	coordinatorID := lab.CoordinatorChatID
	a.mu.Unlock()

	prompt := buildLabAnalysisPrompt(chats)
	messages := []chatMessage{
		{Role: "system", Content: labAnalysisSystemPrompt},
		{Role: "user", Content: prompt},
	}
	completion, err := a.client.doChatCompletion(ctx, a.client.model, messages, 0.2, 0, nil)
	if err != nil {
		return nil, err
	}
	analysis := strings.TrimSpace(completion.Choices[0].Message.Content)
	usage := tokenUsageFrom(completion.Usage)
	sentAt := time.Now()

	a.mu.Lock()
	defer a.mu.Unlock()
	chat, ok := a.chats[coordinatorID]
	if !ok {
		return nil, ErrChatNotFound
	}
	chat.appendActiveMessages(AgentMessage{
		Role:          "assistant",
		Content:       analysis,
		CreatedAt:     sentAt,
		Usage:         usage,
		IsLabAnalysis: true,
	})
	chat.addUsage(usage)
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after lab analysis: %v", chat.ID, err)
	}
	log.Printf("agent: lab %s: analysis posted to coordinator chat %s", labID, coordinatorID)

	return a.buildAgentReplyLocked(chat, analysis, usage, sentAt, sentAt), nil
}

// buildLabAnalysisPrompt renders every lab chat's transcript (and, for
// sticky_facts, its current facts) as plain text for the analysis call.
func buildLabAnalysisPrompt(chats []*Chat) string {
	var sb strings.Builder
	for _, c := range chats {
		sb.WriteString(fmt.Sprintf("### Стратегия: %s (всего токенов: %d", c.ContextStrategy, c.CumulativeTotalTokens))
		if c.CumulativeCostUsd != nil {
			sb.WriteString(fmt.Sprintf(", $%.5f", *c.CumulativeCostUsd))
		}
		sb.WriteString(")\n")
		for _, m := range c.Messages {
			sb.WriteString(fmt.Sprintf("%s: %s\n", m.Role, m.Content))
		}
		if c.ContextStrategy == StrategyStickyFacts && len(c.Facts) > 0 {
			if encoded, err := json.Marshal(c.Facts); err == nil {
				sb.WriteString("facts: " + string(encoded) + "\n")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// LabStore persists every lab as one entry in a single index file — unlike
// ChatStore's one-file-per-chat, a lab is small (a handful of chat IDs) and
// created far less often, so a whole-file read-modify-write on every Save is
// simpler and plenty fast at this scale.
type LabStore struct {
	path string
}

func NewLabStore(dir string) *LabStore {
	return &LabStore{path: filepath.Join(dir, "labs.json")}
}

// Save upserts lab into the index file by ID.
func (s *LabStore) Save(lab *Lab) error {
	labs, err := s.LoadAll()
	if err != nil {
		return err
	}
	replaced := false
	for i, existing := range labs {
		if existing.ID == lab.ID {
			labs[i] = lab
			replaced = true
			break
		}
	}
	if !replaced {
		labs = append(labs, lab)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(labs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// LoadAll reads every persisted lab. A missing index file (first run) yields
// an empty, non-error result.
func (s *LabStore) LoadAll() ([]*Lab, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var labs []*Lab
	if err := json.Unmarshal(data, &labs); err != nil {
		return nil, err
	}
	return labs, nil
}
