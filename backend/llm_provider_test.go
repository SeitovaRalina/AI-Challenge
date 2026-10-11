package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestLocalChatRoutesEveryCompletionLocallyAndPersistsProvider(t *testing.T) {
	var localCalls, cloudCalls atomic.Int32
	server := func(counter *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			if r.URL.Path != "/v1/chat/completions" {
				t.Errorf("unexpected LLM path: %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{
					"message":       map[string]string{"role": "assistant", "content": `{"reply":"Готово","estimate":null,"invariant_conflict":[],"citations":[]}`},
					"finish_reason": "stop",
				}},
				"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
			})
		}))
	}
	local := server(&localCalls)
	defer local.Close()
	cloud := server(&cloudCalls)
	defer cloud.Close()

	root := t.TempDir()
	newAgent := func() *Agent {
		agent := NewAgent(
			NewLiteLLMClient(cloud.URL, "test", "cloud-model"),
			NewChatStore(filepath.Join(root, "sessions")),
			NewLabStore(filepath.Join(root, "labs")),
			NewProjectStore(filepath.Join(root, "projects")),
			NewProfileStore(filepath.Join(root, "profile.json")),
			4096, 10, StrategySlidingWindow,
		)
		agent.localClient = NewLiteLLMClient(local.URL, "ollama", "local-model")
		return agent
	}

	agent := newAgent()
	chat, err := agent.CreateChatWithProvider("", ProviderOllama)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := agent.PostMessage(context.Background(), chat.ID, "Оцени задачу", false)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Provider != ProviderOllama || localCalls.Load() < 2 || cloudCalls.Load() != 0 {
		t.Fatalf("reply provider=%q, local calls=%d, cloud calls=%d", reply.Provider, localCalls.Load(), cloudCalls.Load())
	}
	reloaded, err := newAgent().GetChat(chat.ID)
	if err != nil || reloaded.Provider != ProviderOllama {
		t.Fatalf("reloaded chat=%+v, error=%v", reloaded, err)
	}
}

func TestBareEstimateResponseBecomesChatReply(t *testing.T) {
	turn, err := parseAgentTurn(`{"summary":"Экспорт CSV","category":"функция","complexity":"low","estimated_hours_min":1,"estimated_hours_max":2,"risks":[],"assumptions":[],"subtasks":[]}`)
	if err != nil || turn.Estimate == nil || turn.Reply == "" {
		t.Fatalf("turn=%+v, error=%v", turn, err)
	}
}
