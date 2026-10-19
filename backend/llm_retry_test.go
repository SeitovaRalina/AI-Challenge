package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLiteLLMCompletionRetriesTemporaryGatewayError(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization header: %q", got)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"message":"Upstream websocket closed before response.completed"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":12}}`)
	}))
	defer server.Close()

	client := NewLiteLLMClient(server.URL, "test-key", "test-model")
	result, err := client.doChatCompletion(context.Background(), client.model, []chatMessage{{Role: "user", Content: "task"}}, 0.2, 0, nil)
	if err != nil || result.Choices[0].Message.Content != "ok" || calls.Load() != 2 {
		t.Fatalf("completion=%v, error=%v, calls=%d", result, err, calls.Load())
	}
}

func TestLiteLLMCompletionDoesNotRetryAuthenticationError(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewLiteLLMClient(server.URL, "bad-key", "test-model")
	_, err := client.doChatCompletion(context.Background(), client.model, []chatMessage{{Role: "user", Content: "task"}}, 0.2, 0, nil)
	if !errors.Is(err, ErrUpstreamAuth) || calls.Load() != 1 {
		t.Fatalf("error=%v, calls=%d", err, calls.Load())
	}
}

func TestPersonalizedEstimateStreamSurvivesGateway502(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"message":"Upstream websocket closed before response.completed"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"reply\":\"Оценка готова\",\"estimate\":{\"summary\":\"CSV export\",\"category\":\"backend\",\"complexity\":\"low\",\"estimated_hours_min\":2,\"estimated_hours_max\":4,\"risks\":[],\"assumptions\":[],\"subtasks\":[]},\"citations\":[],\"invariant_conflict\":[]}"},"finish_reason":"stop"}],"usage":{"total_tokens":30}}`)
	}))
	defer server.Close()

	client := NewLiteLLMClient(server.URL, "test-key", "test-model")
	store := NewChatStore(t.TempDir())
	chat := &Chat{ID: "stream-retry", Title: "Новый чат", Provider: ProviderLiteLLM, ContextStrategy: StrategySlidingWindow, Messages: []AgentMessage{}}
	agent := &Agent{client: client, store: store, chats: map[string]*Chat{chat.ID: chat}, profile: &UserProfile{}}
	request := httptest.NewRequest(http.MethodPost, "/api/agent/chats/stream-retry/messages/stream", strings.NewReader(`{"message":"/estimate-personalized CSV export"}`))
	request.SetPathValue("id", chat.ID)
	response := httptest.NewRecorder()
	streamAgentMessageHandler(agent)(response, request)

	body := response.Body.String()
	if calls.Load() != 2 || !strings.Contains(body, "event: done") || strings.Contains(body, "event: error") {
		t.Fatalf("stream did not recover from 502: calls=%d body=%s", calls.Load(), body)
	}
	if chat.Estimate == nil || chat.Estimate.EstimatedHoursMax != 4 {
		t.Fatalf("structured estimate not saved: %+v", chat.Estimate)
	}
}
