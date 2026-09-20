package main

import (
	"fmt"
	"log"
)

// TaskStage is one node of day 13's task state machine — the process a
// chat's task is going through, distinct from what the agent knows about it
// (TaskMemory, day 11) or who's asking (UserProfile, day 12). Unlike the
// generic planning/execution/validation/done example from the assignment,
// these map onto this product's actual estimation loop: there is no code
// execution here, only description → clarification → estimate → acceptance.
type TaskStage string

const (
	TaskStageIntake     TaskStage = "intake"
	TaskStageClarifying TaskStage = "clarifying"
	TaskStageEstimated  TaskStage = "estimated"
	TaskStageDone       TaskStage = "done"
)

// TaskState is what the UI and the model are told about a chat's current
// stage: the stage itself, a short human-readable description of what's
// happening right now, and what the user is expected to do next.
type TaskState struct {
	Stage          TaskStage `json:"stage"`
	Step           string    `json:"step"`
	ExpectedAction string    `json:"expected_action"`
}

// computeTaskState is pure and deterministic: the stage is always derived
// from c.activeMessages()/c.activeEstimate()/c.TaskDone/c.EstimateRevisions,
// never stored on its own, so it can never drift from what actually
// happened — the same lesson day 12's self-managed interview learned the
// hard way (three straight prompt-wording failures) applied structurally
// here instead: the model is told the stage, it never decides it.
func computeTaskState(c *Chat) TaskState {
	switch {
	case len(c.activeMessages()) == 0:
		return TaskState{
			Stage:          TaskStageIntake,
			Step:           "Ожидание описания задачи",
			ExpectedAction: "Опишите задачу, которую нужно оценить",
		}
	case c.activeEstimate() == nil:
		return TaskState{
			Stage:          TaskStageClarifying,
			Step:           "Уточнение деталей задачи",
			ExpectedAction: "Ответьте на уточняющий вопрос ассистента",
		}
	case c.TaskDone:
		return TaskState{
			Stage:          TaskStageDone,
			Step:           fmt.Sprintf("Оценка принята (ревизия №%d)", c.EstimateRevisions),
			ExpectedAction: "Задача оценена — можно начать новую",
		}
	default:
		return TaskState{
			Stage:          TaskStageEstimated,
			Step:           fmt.Sprintf("Оценка сформирована (ревизия №%d)", c.EstimateRevisions),
			ExpectedAction: "Проверьте оценку: уточните детали или подтвердите её",
		}
	}
}

// taskStateSystemPrompt tells the model where the conversation stands right
// now, so a chat resumed after a pause doesn't get re-greeted or re-asked
// things already settled (day 13's "продолжение без повторных объяснений"
// check) — a one-shot injection, not something the model is asked to track
// or update itself.
func taskStateSystemPrompt(ts TaskState) string {
	return fmt.Sprintf(
		"Текущий этап задачи (формальное состояние интерфейса, считается автоматически — не меняй его и не упоминай явно как техническое поле): %s. Текущий шаг: %s. Ожидаемое действие пользователя: %s. Разговор мог быть на паузе — если в истории уже есть нужный контекст, не здоровайся заново и не переобъясняй то, что уже обсуждено, отвечай по существу на новое сообщение.",
		ts.Stage, ts.Step, ts.ExpectedAction,
	)
}

// ErrNoEstimateYet is returned by SetTaskDone when the caller tries to
// accept a task before any estimate exists.
var ErrNoEstimateYet = fmt.Errorf("agent: cannot accept a task with no estimate yet")

// SetTaskDone is the manual accept/reopen action — the explicit counterpart
// to the automatic estimated→done transition being denied to the model
// itself, mirroring UpdateProfile/UpdateChatTask's discipline that some
// state changes are only ever user-driven, never inferred.
func (a *Agent) SetTaskDone(chatID string, done bool) (TaskState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	chat, ok := a.chats[chatID]
	if !ok {
		return TaskState{}, ErrChatNotFound
	}
	if done && chat.activeEstimate() == nil {
		return TaskState{}, ErrNoEstimateYet
	}
	chat.TaskDone = done
	if err := a.store.Save(chat); err != nil {
		log.Printf("agent: failed to persist chat %s task state: %v", chat.ID, err)
	}
	state := computeTaskState(chat)
	log.Printf("agent: chat %s: task state set to %s", chat.ID, state.Stage)
	return state, nil
}
