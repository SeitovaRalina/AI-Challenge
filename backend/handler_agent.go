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
// strategy, branching (checkpoints/branches/active branch), and labs (the
// day-10 side-by-side strategy comparison). Day 1-6's one-shot comparison
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
	default:
		writeLLMError(w, err)
	}
}

// agentMessageRequest is the payload accepted by POST /api/agent/chats/{id}/messages.
type agentMessageRequest struct {
	Message string `json:"message"`
}

// createChatHandler starts a new, empty chat and returns its summary.
func createChatHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, agent.CreateChat())
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
		writeJSON(w, http.StatusOK, chatDetail(chat, agent.contextTokenLimit, agent.historyKeepLastN))
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

		reply, err := agent.PostMessageWithFanOut(ctx, chatID, message)
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
		writeJSON(w, http.StatusOK, chatDetail(chat, agent.contextTokenLimit, agent.historyKeepLastN))
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
			Chat:       chatDetail(chat, agent.contextTokenLimit, agent.historyKeepLastN),
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
		writeJSON(w, http.StatusOK, chatDetail(chat, agent.contextTokenLimit, agent.historyKeepLastN))
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
		writeJSON(w, http.StatusOK, chatDetail(chat, agent.contextTokenLimit, agent.historyKeepLastN))
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
		writeJSON(w, http.StatusOK, chatDetail(chat, agent.contextTokenLimit, agent.historyKeepLastN))
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
