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
