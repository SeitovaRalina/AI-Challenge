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
