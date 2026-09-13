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

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func estimateHandler(client *LiteLLMClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "метод не поддерживается")
			return
		}

		var req EstimateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		task := strings.TrimSpace(req.Task)
		if task == "" {
			writeError(w, http.StatusBadRequest, "задача не может быть пустой")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		estimate, err := client.Estimate(ctx, task)
		if err != nil {
			log.Printf("estimate request failed: %v", err)
			switch {
			case errors.Is(err, ErrUpstreamAuth):
				writeError(w, http.StatusBadGateway, "ошибка авторизации LLM")
			case errors.Is(err, ErrUpstreamUnavailable):
				writeError(w, http.StatusBadGateway, "сервис LLM сейчас недоступен")
			case errors.Is(err, ErrInvalidOutput):
				writeError(w, http.StatusBadGateway, "LLM вернул некорректную оценку")
			default:
				writeError(w, http.StatusInternalServerError, "внутренняя ошибка")
			}
			return
		}

		writeJSON(w, http.StatusOK, estimate)
	}
}

func compareHandler(client *LiteLLMClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "метод не поддерживается")
			return
		}

		var req CompareRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		task := strings.TrimSpace(req.Task)
		if task == "" {
			writeError(w, http.StatusBadRequest, "задача не может быть пустой")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()

		comparison, err := client.CompareFormats(ctx, task, req.Options())
		if err != nil {
			log.Printf("compare request failed: %v", err)
			switch {
			case errors.Is(err, ErrUpstreamAuth):
				writeError(w, http.StatusBadGateway, "ошибка авторизации LLM")
			case errors.Is(err, ErrUpstreamUnavailable):
				writeError(w, http.StatusBadGateway, "сервис LLM сейчас недоступен")
			case errors.Is(err, ErrInvalidOutput):
				writeError(w, http.StatusBadGateway, "LLM вернул некорректную оценку")
			default:
				writeError(w, http.StatusInternalServerError, "внутренняя ошибка")
			}
			return
		}

		writeJSON(w, http.StatusOK, comparison)
	}
}

// compareControlledHandler re-runs only the "controlled" side of the day-2
// comparison. It exists so the frontend can re-test controlled parameters
// against the same task without re-generating the fixed "uncontrolled" side.
func compareControlledHandler(client *LiteLLMClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "метод не поддерживается")
			return
		}

		var req CompareRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		task := strings.TrimSpace(req.Task)
		if task == "" {
			writeError(w, http.StatusBadRequest, "задача не может быть пустой")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		result, err := client.ControlledEstimate(ctx, task, req.Options())
		if err != nil {
			log.Printf("controlled compare request failed: %v", err)
			switch {
			case errors.Is(err, ErrUpstreamAuth):
				writeError(w, http.StatusBadGateway, "ошибка авторизации LLM")
			case errors.Is(err, ErrUpstreamUnavailable):
				writeError(w, http.StatusBadGateway, "сервис LLM сейчас недоступен")
			case errors.Is(err, ErrInvalidOutput):
				writeError(w, http.StatusBadGateway, "LLM вернул некорректную оценку")
			default:
				writeError(w, http.StatusInternalServerError, "внутренняя ошибка")
			}
			return
		}

		writeJSON(w, http.StatusOK, result)
	}
}

// temperatureHandler runs the day-4 temperature comparison: the same task
// solved at temperature 0, 0.7, and 1.2, plus an LLM analysis of accuracy,
// creativity, and diversity for each.
func temperatureHandler(client *LiteLLMClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "метод не поддерживается")
			return
		}

		var req TemperatureRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		task := strings.TrimSpace(req.Task)
		if task == "" {
			writeError(w, http.StatusBadRequest, "задача не может быть пустой")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()

		result, err := client.CompareTemperatures(ctx, task)
		if err != nil {
			log.Printf("temperature request failed: %v", err)
			switch {
			case errors.Is(err, ErrUpstreamAuth):
				writeError(w, http.StatusBadGateway, "ошибка авторизации LLM")
			case errors.Is(err, ErrUpstreamUnavailable):
				writeError(w, http.StatusBadGateway, "сервис LLM сейчас недоступен")
			case errors.Is(err, ErrInvalidOutput):
				writeError(w, http.StatusBadGateway, "LLM вернул некорректную оценку")
			default:
				writeError(w, http.StatusInternalServerError, "внутренняя ошибка")
			}
			return
		}

		writeJSON(w, http.StatusOK, result)
	}
}

// reasoningHandler runs the day-3 reasoning-strategy comparison: the same
// task solved directly, step-by-step, via meta-prompting, and by an expert
// panel, so the four results can be compared side by side.
func reasoningHandler(client *LiteLLMClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "метод не поддерживается")
			return
		}

		var req ReasoningRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		task := strings.TrimSpace(req.Task)
		if task == "" {
			writeError(w, http.StatusBadRequest, "задача не может быть пустой")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()

		result, err := client.CompareReasoning(ctx, task)
		if err != nil {
			log.Printf("reasoning request failed: %v", err)
			switch {
			case errors.Is(err, ErrUpstreamAuth):
				writeError(w, http.StatusBadGateway, "ошибка авторизации LLM")
			case errors.Is(err, ErrUpstreamUnavailable):
				writeError(w, http.StatusBadGateway, "сервис LLM сейчас недоступен")
			case errors.Is(err, ErrInvalidOutput):
				writeError(w, http.StatusBadGateway, "LLM вернул некорректную оценку")
			default:
				writeError(w, http.StatusInternalServerError, "внутренняя ошибка")
			}
			return
		}

		writeJSON(w, http.StatusOK, result)
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
			writeError(w, http.StatusNotFound, "чат не найден")
			return
		}
		writeJSON(w, http.StatusOK, chatDetail(chat, agent.contextTokenLimit))
	}
}

// deleteChatHandler permanently removes a chat and its history.
func deleteChatHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")
		if err := agent.DeleteChat(chatID); err != nil {
			writeError(w, http.StatusNotFound, "чат не найден")
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
			writeError(w, http.StatusNotFound, "чат не найден")
			return
		}
		writeJSON(w, http.StatusOK, summary)
	}
}

// postAgentMessageHandler sends one chat message through the Agent: it
// appends to that chat's own history, calls the LLM with the full
// conversation so far, and returns the assistant's reply plus the chat's
// current estimate.
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

		reply, err := agent.PostMessage(ctx, chatID, message)
		if err != nil {
			log.Printf("agent message failed: %v", err)
			switch {
			case errors.Is(err, ErrChatNotFound):
				writeError(w, http.StatusNotFound, "чат не найден")
			case errors.Is(err, ErrUpstreamAuth):
				writeError(w, http.StatusBadGateway, "ошибка авторизации LLM")
			case errors.Is(err, ErrUpstreamUnavailable):
				writeError(w, http.StatusBadGateway, "сервис LLM сейчас недоступен")
			case errors.Is(err, ErrInvalidOutput):
				writeError(w, http.StatusBadGateway, "LLM вернул некорректный ответ")
			default:
				writeError(w, http.StatusInternalServerError, "внутренняя ошибка")
			}
			return
		}

		writeJSON(w, http.StatusOK, reply)
	}
}

// modelsHandler runs the day-5 model-version comparison: the same task
// solved by three models of increasing capability tier (weak, medium,
// strong), so measured performance and judged answer quality can be
// compared side by side.
func modelsHandler(client *LiteLLMClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "метод не поддерживается")
			return
		}

		var req ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}

		task := strings.TrimSpace(req.Task)
		if task == "" {
			writeError(w, http.StatusBadRequest, "задача не может быть пустой")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()

		result, err := client.CompareModels(ctx, task)
		if err != nil {
			log.Printf("model comparison request failed: %v", err)
			switch {
			case errors.Is(err, ErrUpstreamAuth):
				writeError(w, http.StatusBadGateway, "ошибка авторизации LLM")
			case errors.Is(err, ErrUpstreamUnavailable):
				writeError(w, http.StatusBadGateway, "сервис LLM сейчас недоступен")
			case errors.Is(err, ErrInvalidOutput):
				writeError(w, http.StatusBadGateway, "LLM вернул некорректную оценку")
			default:
				writeError(w, http.StatusInternalServerError, "внутренняя ошибка")
			}
			return
		}

		writeJSON(w, http.StatusOK, result)
	}
}
