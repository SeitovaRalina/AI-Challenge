package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// Handlers for the day 7+ chat agent: chats themselves, their context
// strategy, branching (checkpoints/branches/active branch), labs (the
// day-10 side-by-side strategy comparison), and projects (the day-11
// long-term memory layer — see project.go). Day 1-6's one-shot comparison
// endpoints live in handler.go.

// writeAgentError maps the agent-layer sentinel errors shared across these
// handlers to an HTTP status and Russian message, on top of writeLLMError's
// upstream-LLM cases.
func writeAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrChatNotFound):
		writeError(w, http.StatusNotFound, "чат не найден")
	case errors.Is(err, ErrLabNotFound):
		writeError(w, http.StatusNotFound, "лаборатория не найдена")
	case errors.Is(err, ErrBranchNotFound):
		writeError(w, http.StatusNotFound, "ветка не найдена")
	case errors.Is(err, ErrWrongStrategy):
		writeError(w, http.StatusBadRequest, "недоступно для текущей стратегии контекста")
	case errors.Is(err, ErrFanOutInProgress):
		writeError(w, http.StatusConflict, "предыдущий фан-аут ещё выполняется, подождите")
	case errors.Is(err, ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "проект не найден")
	case errors.Is(err, ErrNoEstimateYet):
		writeError(w, http.StatusBadRequest, "оценка ещё не сформирована")
	default:
		writeLLMError(w, err)
	}
}

// chatDetailWithLab builds chat's ChatDetail and fills in IsLabCoordinator —
// every handler below that returns a ChatDetail uses this instead of calling
// chatDetail directly, so that field is never forgotten on one path.
func chatDetailWithLab(agent *Agent, chat *Chat) ChatDetail {
	detail := chatDetail(chat, agent.contextTokenLimit, agent.historyKeepLastN)
	detail.IsLabCoordinator = agent.IsLabCoordinator(chat.LabID, chat.ID)
	if detail.IsLabCoordinator {
		detail.FanOut = agent.FanOutStatus(chat.LabID)
	}
	if chat.ProjectID != "" {
		if project, err := agent.GetProject(chat.ProjectID); err == nil {
			detail.Project = project
		}
	}
	detail.Profile = agent.GetProfile()
	return detail
}

// agentMessageRequest is the payload accepted by POST /api/agent/chats/{id}/messages.
// Interview flags this turn as part of day 12's onboarding interview — see
// interviewModeSystemPrompt in agent_turn.go for what that changes and why.
type agentMessageRequest struct {
	Message   string `json:"message"`
	Interview bool   `json:"interview,omitempty"`
}

// createChatRequest is the payload accepted by POST /api/agent/chats.
// ProjectID is optional — empty means the new chat belongs to no project,
// exactly like before day 11.
type createChatRequest struct {
	ProjectID string `json:"project_id,omitempty"`
}

// createChatHandler starts a new, empty chat — optionally scoped to an
// existing project — and returns its summary.
func createChatHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		summary, err := agent.CreateChat(strings.TrimSpace(req.ProjectID))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, summary)
	}
}

// listChatsHandler returns every chat's summary, so the sidebar chat list can
// be populated and a chat resumed by selecting it.
func listChatsHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, agent.ListChats())
	}
}

// getChatHandler returns one chat's full history and current estimate.
func getChatHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")
		chat, err := agent.GetChat(chatID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, chatDetailWithLab(agent, chat))
	}
}

// deleteChatHandler permanently removes a chat and its history.
func deleteChatHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")
		if err := agent.DeleteChat(chatID); err != nil {
			writeAgentError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// renameChatRequest is the payload accepted by PATCH /api/agent/chats/{id}.
type renameChatRequest struct {
	Title string `json:"title"`
}

// renameChatHandler sets a chat's display title to a user-chosen value.
func renameChatHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		var req renameChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		title := strings.TrimSpace(req.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "название не может быть пустым")
			return
		}

		summary, err := agent.RenameChat(chatID, title)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, summary)
	}
}

// postAgentMessageHandler sends one chat message through the Agent: it
// appends to that chat's own history (or, for branching, its active
// branch), calls the LLM with that strategy's own view of the conversation
// so far, and returns the assistant's reply plus the chat's current
// estimate. When chatID is a lab's coordinator chat, the same message is
// also fanned out in the background to every other chat in that lab.
func postAgentMessageHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		var req agentMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		message := strings.TrimSpace(req.Message)
		if message == "" {
			writeError(w, http.StatusBadRequest, "сообщение не может быть пустым")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		reply, err := agent.PostChatMessage(ctx, chatID, message, req.Interview)
		if err != nil {
			log.Printf("agent message failed: %v", err)
			writeAgentError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, reply)
	}
}

// setStrategyRequest is the payload accepted by
// PATCH /api/agent/chats/{id}/strategy.
type setStrategyRequest struct {
	Strategy ContextStrategy `json:"strategy"`
}

// setStrategyHandler switches one chat's context strategy live, so the same
// conversation can be compared under different strategies without starting
// a new chat.
func setStrategyHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		var req setStrategyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		if !req.Strategy.IsValid() {
			writeError(w, http.StatusBadRequest, "неизвестная стратегия контекста")
			return
		}

		chat, err := agent.SetContextStrategy(chatID, req.Strategy)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, chatDetailWithLab(agent, chat))
	}
}

// forceCompressResponse reports whether the /compress command actually found
// anything to fold (false when the raw tail is already at or below the
// configured keep-window), alongside the chat's resulting full state.
type forceCompressResponse struct {
	Compressed bool       `json:"compressed"`
	Chat       ChatDetail `json:"chat"`
}

// compressChatHandler forces an immediate history-compression pass for one
// rolling_summary chat (the /compress command), instead of waiting for the
// automatic 2*historyKeepLastN trigger.
func compressChatHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		compressed, err := agent.ForceCompress(ctx, chatID)
		if err != nil {
			log.Printf("force compress failed: %v", err)
			writeAgentError(w, err)
			return
		}

		chat, err := agent.GetChat(chatID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, forceCompressResponse{
			Compressed: compressed,
			Chat:       chatDetailWithLab(agent, chat),
		})
	}
}

// createCheckpointRequest is the payload accepted by
// POST /api/agent/chats/{id}/checkpoints.
type createCheckpointRequest struct {
	Label string `json:"label"`
}

// createCheckpointHandler marks the active branch's current message count as
// a point new branches can later be forked from.
func createCheckpointHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		var req createCheckpointRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		label := strings.TrimSpace(req.Label)
		if label == "" {
			label = "checkpoint"
		}

		chat, err := agent.CreateCheckpoint(chatID, label)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, chatDetailWithLab(agent, chat))
	}
}

// createBranchRequest is the payload accepted by
// POST /api/agent/chats/{id}/branches.
type createBranchRequest struct {
	CheckpointIndex int    `json:"checkpoint_index"`
	FromBranchID    string `json:"from_branch_id"`
	Label           string `json:"label"`
}

// createBranchHandler forks a new branch from an existing branch's messages
// up to checkpoint_index. Calling this twice with the same
// from_branch_id/checkpoint_index and different labels is how "2 branches
// from one point" is done.
func createBranchHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		var req createBranchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		label := strings.TrimSpace(req.Label)
		if label == "" {
			writeError(w, http.StatusBadRequest, "название ветки не может быть пустым")
			return
		}
		fromBranchID := strings.TrimSpace(req.FromBranchID)
		if fromBranchID == "" {
			fromBranchID = "main"
		}

		chat, err := agent.CreateBranch(chatID, req.CheckpointIndex, fromBranchID, label)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, chatDetailWithLab(agent, chat))
	}
}

// setActiveBranchRequest is the payload accepted by
// PATCH /api/agent/chats/{id}/active-branch.
type setActiveBranchRequest struct {
	BranchID string `json:"branch_id"`
}

// setActiveBranchHandler switches which branch a chat's messages/PostMessage
// operate on.
func setActiveBranchHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		var req setActiveBranchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		branchID := strings.TrimSpace(req.BranchID)
		if branchID == "" {
			writeError(w, http.StatusBadRequest, "не указана ветка")
			return
		}

		chat, err := agent.SetActiveBranch(chatID, branchID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, chatDetailWithLab(agent, chat))
	}
}

// createLabRequest is the payload accepted by POST /api/labs.
type createLabRequest struct {
	Label string `json:"label"`
}

// createLabResponse is a new lab's identity plus its freshly created chats,
// so the sidebar can render the lab group immediately without a reload.
type createLabResponse struct {
	Lab   *Lab          `json:"lab"`
	Chats []ChatSummary `json:"chats"`
}

// createLabHandler creates a new Lab: one chat per strategy in
// labStrategies, tagged with the given label, grouped together.
func createLabHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createLabRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		label := strings.TrimSpace(req.Label)
		if label == "" {
			writeError(w, http.StatusBadRequest, "название лаборатории не может быть пустым")
			return
		}

		lab, chats, err := agent.CreateLab(label)
		if err != nil {
			log.Printf("create lab failed: %v", err)
			writeError(w, http.StatusInternalServerError, "внутренняя ошибка")
			return
		}
		writeJSON(w, http.StatusCreated, createLabResponse{Lab: lab, Chats: chats})
	}
}

// analyzeLabHandler runs the /analyze comparison across a lab's chats and
// appends the result to its coordinator chat.
func analyzeLabHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		labID := r.PathValue("id")

		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()

		reply, err := agent.AnalyzeLab(ctx, labID)
		if err != nil {
			log.Printf("lab analysis failed: %v", err)
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, reply)
	}
}

// deleteLabHandler removes a lab and every chat it owns.
func deleteLabHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		labID := r.PathValue("id")
		if err := agent.DeleteLab(labID); err != nil {
			writeAgentError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// createProjectRequest is the payload accepted by POST /api/projects.
type createProjectRequest struct {
	Name string `json:"name"`
}

// createProjectHandler creates a new, empty project (day 11's long-term
// memory layer — see project.go).
func createProjectHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "название проекта не может быть пустым")
			return
		}

		project, err := agent.CreateProject(name)
		if err != nil {
			log.Printf("create project failed: %v", err)
			writeError(w, http.StatusInternalServerError, "внутренняя ошибка")
			return
		}
		writeJSON(w, http.StatusCreated, project)
	}
}

// listProjectsHandler returns every project, so the sidebar can group chats
// by project.
func listProjectsHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, agent.ListProjects())
	}
}

// getProjectHandler returns one project's full state, including its
// long-term memory (known_stack/notes).
func getProjectHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")
		project, err := agent.GetProject(projectID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, project)
	}
}

// deleteProjectHandler removes a project. Its chats are kept — only their
// project_id is cleared (see Agent.DeleteProject).
func deleteProjectHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")
		if err := agent.DeleteProject(projectID); err != nil {
			writeAgentError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// updateProjectMemoryRequest is the payload accepted by
// PATCH /api/projects/{id}/memory.
type updateProjectMemoryRequest struct {
	KnownStack []string `json:"known_stack"`
	Notes      []string `json:"notes"`
}

// updateProjectMemoryHandler lets the user manually add, edit, or delete a
// project's long-term memory — the explicit counterpart to the automatic
// per-turn extraction.
func updateProjectMemoryHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")

		var req updateProjectMemoryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		project, err := agent.UpdateProjectMemory(projectID, req.KnownStack, req.Notes)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, project)
	}
}

// updateProjectInvariantsRequest is the payload accepted by
// PATCH /api/projects/{id}/invariants.
type updateProjectInvariantsRequest struct {
	Invariants []string `json:"invariants"`
}

// updateProjectInvariantsHandler lets the user manually add, edit, or remove
// a project's invariants (day 14) — the ONLY way to shrink the list, since
// the automatic per-turn extraction (memory_invariants.go) never removes an
// entry itself.
func updateProjectInvariantsHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")

		var req updateProjectInvariantsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		project, err := agent.UpdateProjectInvariants(projectID, req.Invariants)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, project)
	}
}

// updateChatTaskRequest is the payload accepted by
// PATCH /api/agent/chats/{id}/task.
type updateChatTaskRequest struct {
	Goal              string            `json:"goal"`
	Constraints       []string          `json:"constraints"`
	ClarifyingAnswers map[string]string `json:"clarifying_answers"`
}

// updateChatTaskHandler lets the user manually add, edit, or delete a
// chat's own working memory — the explicit counterpart to the automatic
// per-turn extraction.
func updateChatTaskHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		var req updateChatTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		task, err := agent.UpdateChatTask(chatID, TaskMemory{
			Goal:              req.Goal,
			Constraints:       req.Constraints,
			ClarifyingAnswers: req.ClarifyingAnswers,
		})
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	}
}

// updateTaskStateRequest is the payload accepted by
// PATCH /api/agent/chats/{id}/task-state.
type updateTaskStateRequest struct {
	Done bool `json:"done"`
}

// updateTaskStateHandler is day 13's manual accept/reopen action — the only
// path that ever moves a chat between "estimated" and "done" (the model is
// never asked to decide this itself; see task_state.go).
func updateTaskStateHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		var req updateTaskStateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		state, err := agent.SetTaskDone(chatID, req.Done)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	}
}

// getProfileHandler returns the single global user profile (day 12).
func getProfileHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, agent.GetProfile())
	}
}

// updateProfileRequest is the payload accepted by PATCH /api/profile.
type updateProfileRequest struct {
	Name        string   `json:"name"`
	Stack       []string `json:"stack"`
	Style       string   `json:"style"`
	Format      string   `json:"format"`
	Constraints []string `json:"constraints"`
}

// updateProfileHandler lets the user manually add, edit, or delete the
// global profile — the explicit counterpart to the automatic per-turn
// extraction in memory_profile.go, and the only path for Name (the model
// never infers it — see profileSystemPrompt).
func updateProfileHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req updateProfileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		profile, err := agent.UpdateProfile(req.Name, req.Stack, req.Style, req.Format, req.Constraints)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, profile)
	}
}
