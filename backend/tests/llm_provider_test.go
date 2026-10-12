package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const bareEstimate = `{"summary":"Экспорт CSV","category":"функция","complexity":"low","estimated_hours_min":1,"estimated_hours_max":2,"risks":[],"assumptions":[],"subtasks":[]}`

// This package lives outside package main, so it checks the real HTTP
// contract and persisted files without depending on unexported app details.
func TestChatProviderRoutingAndPersistence(t *testing.T) {
	var localCalls, cloudCalls atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]string{"name": "local-model"}}})
		case "/api/ps":
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]any{"name": "local-model", "size": 2000000000, "size_vram": 0, "context_length": 4096, "details": map[string]string{"quantization_level": "Q4_K_M"}}}})
		case "/v1/chat/completions":
			localCalls.Add(1)
			var payload struct {
				MaxTokens      int     `json:"max_tokens"`
				Temperature    float64 `json:"temperature"`
				ResponseFormat *struct {
					Type string `json:"type"`
				} `json:"response_format"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode local request: %v", err)
			}
			if payload.MaxTokens == 1024 && (payload.ResponseFormat == nil || payload.ResponseFormat.Type != "json_object" || payload.Temperature != 0.1) {
				t.Errorf("local answer settings: %+v", payload)
			}
			writeCompletion(t, w, bareEstimate)
		default:
			t.Errorf("unexpected Ollama path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer local.Close()
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected LiteLLM path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		cloudCalls.Add(1)
		writeCompletion(t, w, `{"reply":"Готово","estimate":null,"invariant_conflict":[],"citations":[]}`)
	}))
	defer cloud.Close()

	backendDir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	binary := filepath.Join(workDir, "backend")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = backendDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build backend: %v\n%s", err, output)
	}

	start := func(cloudConfigured bool) (string, func()) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		var logs bytes.Buffer
		cmd := exec.Command(binary)
		cmd.Dir = workDir // No user .env file is loaded.
		cmd.Env = append(os.Environ(),
			"PORT="+strconv.Itoa(port),
			"CHAT_DATA_DIR="+filepath.Join(workDir, "sessions"),
			"OLLAMA_CHAT_URL="+local.URL,
			"OLLAMA_CHAT_MODEL=local-model",
			"GITHUB_TOKEN=",
			"CALDAV_USERNAME=",
			"CALDAV_APP_PASSWORD=",
			"COLLECT_INTERVAL=off",
			"MCP_WORKLOG_COMMAND=nonexistent-worklog-command",
		)
		if cloudConfigured {
			cmd.Env = append(cmd.Env, "LITELLM_BASE_URL="+cloud.URL, "LITELLM_API_KEY=test-only-key", "LITELLM_MODEL=cloud-model")
		} else {
			cmd.Env = append(cmd.Env, "LITELLM_API_KEY=", "LITELLM_MODEL=")
		}
		cmd.Stdout, cmd.Stderr = &logs, &logs
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		var once sync.Once
		stop := func() {
			once.Do(func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
		}
		t.Cleanup(stop)
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := http.Get(base + "/api/llm/providers")
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return base, stop
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		stop()
		t.Fatalf("backend did not start:\n%s", logs.String())
		return "", stop
	}

	client := &http.Client{Timeout: 10 * time.Second}
	request := func(base, method, path string, payload any, out any) int {
		t.Helper()
		var body bytes.Buffer
		if payload != nil {
			if err := json.NewEncoder(&body).Encode(payload); err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequest(method, base+path, &body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil && resp.StatusCode < 400 {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatalf("decode %s %s: %v", method, path, err)
			}
		}
		return resp.StatusCode
	}

	base, stop := start(false)
	var created struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	}
	if status := request(base, http.MethodPost, "/api/agent/chats", map[string]string{"provider": "ollama"}, &created); status != http.StatusCreated || created.Provider != "ollama" {
		t.Fatalf("create local chat: status=%d chat=%+v", status, created)
	}
	var reply struct {
		Provider string          `json:"provider"`
		Estimate json.RawMessage `json:"estimate"`
	}
	messagePath := "/api/agent/chats/" + created.ID + "/messages"
	if status := request(base, http.MethodPost, messagePath, map[string]string{"message": "Оцени экспорт CSV"}, &reply); status != http.StatusOK || reply.Provider != "ollama" || string(reply.Estimate) == "null" {
		t.Fatalf("local estimate: status=%d reply=%+v", status, reply)
	}
	var performance struct {
		Loaded        bool   `json:"loaded"`
		Quantization  string `json:"quantization"`
		ContextLength int    `json:"context_length"`
	}
	if status := request(base, http.MethodGet, "/api/llm/performance", nil, &performance); status != http.StatusOK || !performance.Loaded || performance.Quantization != "Q4_K_M" || performance.ContextLength != 4096 {
		t.Fatalf("local performance: status=%d details=%+v", status, performance)
	}
	var withPerformance struct {
		ContextTokenLimit int `json:"context_token_limit"`
		Messages          []struct {
			Performance *struct {
				Provider   string `json:"provider"`
				DurationMS int64  `json:"duration_ms"`
			} `json:"performance"`
		} `json:"messages"`
	}
	if status := request(base, http.MethodGet, "/api/agent/chats/"+created.ID, nil, &withPerformance); status != http.StatusOK || withPerformance.ContextTokenLimit != 4096 || len(withPerformance.Messages) != 2 || withPerformance.Messages[1].Performance == nil || withPerformance.Messages[1].Performance.Provider != "ollama" {
		t.Fatalf("persisted local performance: status=%d chat=%+v", status, withPerformance)
	}
	if localCalls.Load() < 2 || cloudCalls.Load() != 0 {
		t.Fatalf("local turn: local calls=%d cloud calls=%d", localCalls.Load(), cloudCalls.Load())
	}
	if status := request(base, http.MethodPatch, "/api/agent/chats/"+created.ID+"/provider", map[string]string{"provider": "litellm"}, nil); status != http.StatusServiceUnavailable {
		t.Fatalf("cloud switch without credentials: status=%d", status)
	}
	stop()

	base, stop = start(true)
	defer stop()
	var restored struct {
		Provider string            `json:"provider"`
		Messages []json.RawMessage `json:"messages"`
	}
	chatPath := "/api/agent/chats/" + created.ID
	if status := request(base, http.MethodGet, chatPath, nil, &restored); status != http.StatusOK || restored.Provider != "ollama" || len(restored.Messages) != 2 {
		t.Fatalf("restored local chat: status=%d chat=%+v", status, restored)
	}
	beforeLocal := localCalls.Load()
	if status := request(base, http.MethodPatch, chatPath+"/provider", map[string]string{"provider": "litellm"}, nil); status != http.StatusOK {
		t.Fatalf("switch to cloud: status=%d", status)
	}
	if status := request(base, http.MethodPost, messagePath, map[string]string{"message": "Уточнение"}, &reply); status != http.StatusOK || reply.Provider != "litellm" {
		t.Fatalf("cloud turn: status=%d reply=%+v", status, reply)
	}
	if cloudCalls.Load() == 0 || localCalls.Load() != beforeLocal {
		t.Fatalf("after switch: local calls=%d cloud calls=%d", localCalls.Load(), cloudCalls.Load())
	}
}

func writeCompletion(t *testing.T, w http.ResponseWriter, content string) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]string{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
	}); err != nil {
		t.Errorf("write completion: %v", err)
	}
}
