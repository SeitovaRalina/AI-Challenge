package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Lab groups the chats created by "+ Лаборатория": a dispatcher chat
// (CoordinatorChatID) with no strategy of its own, and one real chat per
// strategy in labStrategies (ChatIDs), all fed the same messages — so the
// day-10 assignment's own testing method ("run the same scenario under each
// strategy, compare") is one click instead of manually retyping every
// message into several chats. Only the coordinator accepts direct input
// (see PostChatMessage): the strategy chats exist purely to show each
// strategy's own result, so the comparison always reflects the same input.
type Lab struct {
	ID                string    `json:"id"`
	Label             string    `json:"label"`
	CoordinatorChatID string    `json:"coordinator_chat_id"`
	ChatIDs           []string  `json:"chat_ids"`
	CreatedAt         time.Time `json:"created_at"`
}

// labStrategies is fixed on purpose: exactly the 3 strategies day 10 asks
// for. rolling_summary (day 9) is deliberately left out of labs; it's still
// available as a normal chat's own strategy.
var labStrategies = []ContextStrategy{StrategySlidingWindow, StrategyStickyFacts, StrategyBranching}

// strategyTitle is the strategy chats' own (fixed, never renamed) title —
// capitalized for display, unlike strategyLabel's lowercase form used in the
// analysis prompt's transcript headers.
func strategyTitle(s ContextStrategy) string {
	switch s {
	case StrategySlidingWindow:
		return "Sliding Window"
	case StrategyStickyFacts:
		return "Sticky Facts"
	case StrategyBranching:
		return "Branching"
	case StrategyRollingSummary:
		return "Rolling Summary"
	default:
		return string(s)
	}
}

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

var (
	ErrLabNotFound      = fmt.Errorf("agent: lab not found")
	ErrFanOutInProgress = fmt.Errorf("agent: previous fan-out to this lab is still running")
)

// FanOutStatus is one strategy chat's progress through the coordinator's most
// recent fan-out: "pending" while its PostMessage call is in flight, "done"
// once it has a reply, "failed" (with Error set) if the call errored. The
// coordinator chat's own ChatDetail/AgentReply carries the current list for
// whichever lab it belongs to, so the frontend can render live progress
// without polling a separate endpoint.
type FanOutStatus struct {
	ChatID    string          `json:"chat_id"`
	Strategy  ContextStrategy `json:"strategy"`
	Status    string          `json:"status"` // "pending" | "done" | "failed"
	Error     string          `json:"error,omitempty"`
	StartedAt time.Time       `json:"started_at"`
}

// fanOutInProgressLocked reports whether labID has a fan-out with any chat
// still pending. Callers must hold a.mu.
func (a *Agent) fanOutInProgressLocked(labID string) bool {
	for _, s := range a.fanOut[labID] {
		if s.Status == "pending" {
			return true
		}
	}
	return false
}

// startFanOutLocked records a fresh "pending" entry for every chat in
// lab.ChatIDs, replacing whatever fan-out state labID had before. Callers
// must hold a.mu.
func (a *Agent) startFanOutLocked(lab *Lab) {
	entries := make([]FanOutStatus, 0, len(lab.ChatIDs))
	now := time.Now()
	for i, id := range lab.ChatIDs {
		strategy := ContextStrategy("")
		if i < len(labStrategies) {
			strategy = labStrategies[i]
		}
		entries = append(entries, FanOutStatus{ChatID: id, Strategy: strategy, Status: "pending", StartedAt: now})
	}
	a.fanOut[lab.ID] = entries
}

// fanOutLocked returns labID's current fan-out status list, or nil if it has
// none (never posted to yet, or not a lab chat). Callers must hold a.mu.
func (a *Agent) fanOutLocked(labID string) []FanOutStatus {
	return a.fanOut[labID]
}

// FanOutStatus is fanOutLocked's self-locking counterpart, for callers (like
// chatDetailWithLab) that don't already hold a.mu.
func (a *Agent) FanOutStatus(labID string) []FanOutStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.fanOutLocked(labID)
}

// setFanOutResult updates one chat's fan-out entry once its background
// PostMessage call settles. Self-locking — called from the fan-out goroutine,
// outside a.mu.
func (a *Agent) setFanOutResult(labID, chatID string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entries := a.fanOut[labID]
	for i := range entries {
		if entries[i].ChatID != chatID {
			continue
		}
		if err != nil {
			entries[i].Status = "failed"
			entries[i].Error = err.Error()
		} else {
			entries[i].Status = "done"
		}
		return
	}
}

// IsLabCoordinator reports whether chatID is labID's coordinator chat. Safe
// to call with an empty labID (returns false).
func (a *Agent) IsLabCoordinator(labID, chatID string) bool {
	if labID == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	lab, ok := a.labs[labID]
	return ok && lab.CoordinatorChatID == chatID
}

// CreateLab creates the coordinator chat plus one chat per strategy in
// labStrategies, all grouped under a new Lab, and returns the lab and every
// chat's summary (coordinator first).
func (a *Agent) CreateLab(label string) (*Lab, []ChatSummary, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, nil, fmt.Errorf("agent: lab label must not be empty")
	}

	lab := &Lab{ID: newChatID(), Label: label, CreatedAt: time.Now()}

	a.mu.Lock()
	// Inserted before any chat exists so chatSummary (called below, via the
	// same a.labs map) can already resolve IsLabCoordinator correctly — Lab
	// is a pointer, so filling in CoordinatorChatID/ChatIDs afterward is
	// still visible through this same map entry.
	a.labs[lab.ID] = lab

	coordinator := a.newChatLocked(label, StrategyCoordinator, lab.ID, "")
	lab.CoordinatorChatID = coordinator.ID

	summaries := make([]ChatSummary, 0, len(labStrategies)+1)
	for _, strategy := range labStrategies {
		chat := a.newChatLocked(strategyTitle(strategy), strategy, lab.ID, "")
		lab.ChatIDs = append(lab.ChatIDs, chat.ID)
		summaries = append(summaries, chatSummary(chat, a.labs))
	}
	summaries = append([]ChatSummary{chatSummary(coordinator, a.labs)}, summaries...)
	a.mu.Unlock()

	if err := a.labStore.Save(lab); err != nil {
		log.Printf("agent: failed to persist lab %s: %v", lab.ID, err)
	}
	log.Printf("agent: created lab %s %q, coordinator %s, %d strategy chat(s)", lab.ID, label, lab.CoordinatorChatID, len(lab.ChatIDs))

	return lab, summaries, nil
}

// DeleteLab removes a lab and every chat it owns (coordinator and strategy
// chats alike) — a lab's member chats can't be deleted individually (see
// PostChatMessage/SetContextStrategy's ErrWrongStrategy guards), so this is
// the only way to clean one up.
func (a *Agent) DeleteLab(labID string) error {
	a.mu.Lock()
	lab, ok := a.labs[labID]
	if !ok {
		a.mu.Unlock()
		return ErrLabNotFound
	}
	chatIDs := append([]string{lab.CoordinatorChatID}, lab.ChatIDs...)
	delete(a.labs, labID)
	a.mu.Unlock()

	for _, id := range chatIDs {
		if err := a.DeleteChat(id); err != nil && !errors.Is(err, ErrChatNotFound) {
			log.Printf("agent: failed to delete lab %s chat %s: %v", labID, id, err)
		}
	}
	log.Printf("agent: deleted lab %s", labID)
	return a.labStore.Delete(labID)
}

// PostChatMessage routes a chat message to the right handling: a lab's
// coordinator dispatches and fans out (PostCoordinatorMessage); one of a
// lab's own strategy chats never accepts direct input — only the
// coordinator's fan-out writes to it, so the comparison always reflects the
// same input across every strategy; every other chat is a normal turn.
func (a *Agent) PostChatMessage(ctx context.Context, chatID, userMessage string) (*AgentReply, error) {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok {
		a.mu.Unlock()
		return nil, ErrChatNotFound
	}
	strategy := chat.ContextStrategy
	labID := chat.LabID
	a.mu.Unlock()

	if strategy == StrategyCoordinator {
		return a.PostCoordinatorMessage(ctx, chatID, userMessage)
	}
	if labID != "" {
		return nil, ErrWrongStrategy
	}
	return a.PostMessage(ctx, chatID, userMessage)
}

// PostCoordinatorMessage logs userMessage into the coordinator chat itself
// with a synthetic acknowledgement — the coordinator has no context strategy
// of its own to answer with, so it never calls the LLM for its own turn —
// and fans the same message out in the background to every one of the lab's
// real strategy chats, each through its own PostMessage. Mirrors
// PostMessage's own snapshot/unlock/slow-work/relock shape, even though the
// "slow work" here is just spawning goroutines rather than an LLM call.
func (a *Agent) PostCoordinatorMessage(ctx context.Context, chatID, userMessage string) (*AgentReply, error) {
	a.mu.Lock()
	chat, ok := a.chats[chatID]
	if !ok {
		a.mu.Unlock()
		return nil, ErrChatNotFound
	}
	if chat.ContextStrategy != StrategyCoordinator {
		a.mu.Unlock()
		return nil, ErrWrongStrategy
	}
	lab, labOK := a.labs[chat.LabID]
	if labOK && a.fanOutInProgressLocked(lab.ID) {
		// The 3 strategy chats' history is snapshotted at the start of each
		// PostMessage call — a second fan-out starting before the first one
		// finishes would race two goroutines against the same chat's history,
		// possibly interleaving or losing a turn. Rejecting keeps every
		// strategy chat driven by exactly one fan-out at a time.
		a.mu.Unlock()
		return nil, ErrFanOutInProgress
	}

	userSentAt := time.Now()
	ack := coordinatorAckText(labOK, lab)
	assistantSentAt := time.Now()
	chat.Messages = append(chat.Messages,
		AgentMessage{Role: "user", Content: userMessage, CreatedAt: userSentAt},
		AgentMessage{Role: "assistant", Content: ack, CreatedAt: assistantSentAt},
	)
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s: %v", chat.ID, err)
	}
	if labOK {
		a.startFanOutLocked(lab)
	}
	// The coordinator is never a branching chat — chat.ActiveBranchID (empty)
	// is a fine branchID here, same as it always was.
	agentReply := a.buildAgentReplyLocked(chat, chat.ActiveBranchID, ack, nil, userSentAt, assistantSentAt)
	a.mu.Unlock()

	if labOK {
		// Sequential, not fan-out-in-parallel: three concurrent completions
		// against the same shared LiteLLM key queue behind each other on the
		// proxy side anyway, and were observed timing out under that
		// contention even at a generous per-call budget. One background
		// goroutine calling each strategy chat in turn keeps the coordinator
		// reply instant while giving every strategy its own full budget.
		labID := lab.ID
		chatIDs := append([]string(nil), lab.ChatIDs...)
		go func() {
			for _, id := range chatIDs {
				bgCtx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
				_, err := a.PostMessage(bgCtx, id, userMessage)
				cancel()
				a.setFanOutResult(labID, id, err)
				if err != nil {
					log.Printf("agent: lab fan-out to chat %s failed: %v", id, err)
				}
			}
		}()
	}

	return agentReply, nil
}

func coordinatorAckText(labOK bool, lab *Lab) string {
	if !labOK {
		return "Сообщение получено."
	}
	return fmt.Sprintf("Отправлено в %d стратегии лаборатории «%s».", len(lab.ChatIDs), lab.Label)
}

// labAnalysisSystemPrompt asks the model to compare each strategy's
// transcript along exactly the 4 axes the day-10 assignment names, so the
// resulting message doubles as the assignment's own required comparison.
const labAnalysisSystemPrompt = `You are comparing how the SAME conversation played out under different context-management strategies in a software-task-estimation assistant.

You will see one full transcript per strategy (and, where relevant, its current facts or branch state) plus its token/cost totals. The output is rendered as Markdown with math support, so two formatting rules matter: refer to each strategy only by the plain, space-separated name given in its transcript header (e.g. "sliding window", "sticky facts") — never with an underscore, which is parsed as emphasis — and never write a literal "$" character; write a cost as a plain number followed by "USD" (e.g. "0.00044 USD"), since a pair of "$" anywhere in the text is parsed as a math span and silently deletes everything between them. Compare them along exactly these four points, each its own short labeled paragraph, in Russian:

1. Качество ответов
2. Стабильность (не теряет ли важные детали из начала диалога)
3. Расход токенов
4. Удобство для пользователя

Be concrete — reference actual differences you see in the transcripts and the numbers given, not generic statements that could apply to any comparison. Output plain text only: no JSON, no markdown formatting, no headers beyond the four numbered points themselves.`

// AnalyzeLab gathers every one of labID's strategy chats' full transcript
// and current strategy state (the coordinator itself is never included — it
// has no content of its own to compare), asks the LLM for one comparison
// along the assignment's own 4 axes, and appends that analysis as a real
// assistant message (flagged IsLabAnalysis so the frontend can style it
// distinctly) into the coordinator chat — so the comparison lives in the
// conversation itself, not only in a PR description.
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
	chat.Messages = append(chat.Messages, AgentMessage{
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

	return a.buildAgentReplyLocked(chat, chat.ActiveBranchID, analysis, usage, sentAt, sentAt), nil
}

// buildLabAnalysisPrompt renders every lab strategy chat's transcript (and,
// for sticky_facts, its current facts) as plain text for the analysis call.
func buildLabAnalysisPrompt(chats []*Chat) string {
	var sb strings.Builder
	for _, c := range chats {
		sb.WriteString(fmt.Sprintf("### Стратегия: %s (всего токенов: %d", strategyLabel(c.ContextStrategy), c.CumulativeTotalTokens))
		if c.CumulativeCostUsd != nil {
			sb.WriteString(fmt.Sprintf(", %.5f USD", *c.CumulativeCostUsd))
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
	return s.saveAll(labs)
}

// Delete removes labID from the index file. Deleting a lab that was never
// persisted is not an error.
func (s *LabStore) Delete(labID string) error {
	labs, err := s.LoadAll()
	if err != nil {
		return err
	}
	kept := labs[:0]
	for _, lab := range labs {
		if lab.ID != labID {
			kept = append(kept, lab)
		}
	}
	return s.saveAll(kept)
}

func (s *LabStore) saveAll(labs []*Lab) error {
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
