package main

import (
	"encoding/json"
	"log"
)

// ContextStrategy picks how a chat's history is turned into the prompt sent
// to the model on every turn — see buildContextMessages. sliding_window and
// sticky_facts are windowed by historyKeepLastN; rolling_summary instead
// folds anything older than that window into an LLM-maintained summary
// (day 9); branching sends its active branch's full history, unwindowed.
type ContextStrategy string

const (
	StrategySlidingWindow  ContextStrategy = "sliding_window"
	StrategyStickyFacts    ContextStrategy = "sticky_facts"
	StrategyBranching      ContextStrategy = "branching"
	StrategyRollingSummary ContextStrategy = "rolling_summary"

	// StrategyCoordinator is not a real context-management strategy — it
	// marks a lab's dispatcher chat (see lab.go), which never calls the LLM
	// for its own turn (PostChatMessage routes it to PostCoordinatorMessage
	// instead of PostMessage/buildContextMessages) and is deliberately
	// excluded from IsValid: nothing should ever PATCH a chat onto it by
	// hand, only CreateLab sets it.
	StrategyCoordinator ContextStrategy = "coordinator"
)

// IsValid reports whether s is one of the four real strategies — used to
// reject an unrecognized value from PATCH .../strategy instead of silently
// storing garbage. StrategyCoordinator is intentionally not valid here.
func (s ContextStrategy) IsValid() bool {
	switch s {
	case StrategySlidingWindow, StrategyStickyFacts, StrategyBranching, StrategyRollingSummary:
		return true
	default:
		return false
	}
}

// buildContextMessages turns history (the active branch's messages, for
// branching — Chat.Messages otherwise) into the extra system messages and
// the raw message slice PostMessage resends verbatim, according to
// strategy. facts/summary/summarizedThrough are only read by the strategies
// they belong to.
func buildContextMessages(strategy ContextStrategy, n int, history []AgentMessage, facts map[string]string, summary string, summarizedThrough int) (extra []chatMessage, raw []AgentMessage) {
	switch strategy {
	case StrategyStickyFacts:
		if len(facts) > 0 {
			if encoded, err := json.Marshal(facts); err == nil {
				extra = append(extra, chatMessage{
					Role: "system",
					Content: "Известные факты о задаче (ключ-значение JSON, обновляются после каждого " +
						"сообщения пользователя; используй их как контекст): " + string(encoded),
				})
			}
		}
		return extra, lastNMessages(history, n)

	case StrategyRollingSummary:
		if summarizedThrough > len(history) {
			summarizedThrough = 0
		}
		raw = history[summarizedThrough:]
		if summary != "" {
			extra = append(extra, chatMessage{
				Role: "system",
				Content: "Резюме более ранней части диалога (используй как контекст; " +
					"для точных чисел оценки полагайся на «Текущая актуальная оценка» выше, а не на резюме): " + summary,
			})
		}
		return extra, raw

	case StrategyBranching:
		// The active branch is already scoped to one line of the
		// conversation by construction (forked from a checkpoint, continued
		// independently) — no windowing on top of that.
		return nil, history

	default: // sliding_window, and any unrecognized value — the cheapest, safest fallback.
		return nil, lastNMessages(history, n)
	}
}

// lastNMessages returns the last n messages of history, or all of it if n is
// 0 (windowing disabled) or already covers it.
func lastNMessages(history []AgentMessage, n int) []AgentMessage {
	if n <= 0 || n >= len(history) {
		return history
	}
	return history[len(history)-n:]
}

// SetContextStrategy switches a chat's strategy live, mid-conversation.
// Every strategy's own state (Summary, Facts, Branches) is additive and
// never cleared by switching away from it — switching back later resumes
// from wherever it left off, the same behavior Day 9's compression toggle
// had.
func (a *Agent) SetContextStrategy(chatID string, strategy ContextStrategy) (*Chat, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return nil, ErrChatNotFound
	}
	if chat.LabID != "" {
		// A lab's chats have their strategy fixed by CreateLab — the whole
		// comparison depends on each one staying on the strategy it was
		// created with.
		return nil, ErrWrongStrategy
	}
	chat.ContextStrategy = strategy
	if strategy == StrategyBranching {
		ensureBranchState(chat)
	}
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s after strategy change: %v", chat.ID, err)
	}
	return copyChat(chat), nil
}
