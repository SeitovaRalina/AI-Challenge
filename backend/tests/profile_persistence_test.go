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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalProfileUpdatePreservesSavedFields(t *testing.T) {
	var profileCalls atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]string{"name": "local-model"}}})
		case "/v1/chat/completions":
			var request struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode completion request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			content := `{"reply":"Готово","estimate":null,"invariant_conflict":[],"citations":[]}`
			for _, message := range request.Messages {
				if strings.Contains(message.Content, "You maintain a single global user profile") {
					profileCalls.Add(1)
					content = `{"name":"","stack":[],"style":"","format":"","constraints":[]}`
					break
				}
			}
			writeCompletion(t, w, content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer local.Close()

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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	cmd := exec.Command(binary)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PORT="+fmt.Sprint(port),
		"CHAT_DATA_DIR="+filepath.Join(workDir, "sessions"),
		"OLLAMA_CHAT_URL="+local.URL,
		"OLLAMA_CHAT_MODEL=local-model",
		"LITELLM_API_KEY=",
		"LITELLM_MODEL=",
		"GITHUB_TOKEN=",
		"CALDAV_USERNAME=",
		"CALDAV_APP_PASSWORD=",
		"COLLECT_INTERVAL=off",
		"MCP_WORKLOG_COMMAND=nonexistent-worklog-command",
	)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 30 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/api/llm/providers")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	request := func(method, path string, payload any, out any) {
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
			t.Fatalf("%s %s: %v\n%s", method, path, err, logs.String())
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Fatalf("%s %s: status %d\n%s", method, path, resp.StatusCode, logs.String())
		}
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}

	profile := map[string]any{
		"name": "Ралина", "stack": []string{"Go", "React"},
		"style": "на ты", "format": "кратко", "constraints": []string{"без эмодзи"},
	}
	request(http.MethodPatch, "/api/profile", profile, nil)
	var chat struct {
		ID string `json:"id"`
	}
	request(http.MethodPost, "/api/agent/chats", map[string]string{"provider": "ollama"}, &chat)
	request(http.MethodPost, "/api/agent/chats/"+chat.ID+"/messages", map[string]string{"message": "Оцени экспорт CSV"}, nil)
	var saved struct {
		Name        string   `json:"name"`
		Stack       []string `json:"stack"`
		Style       string   `json:"style"`
		Format      string   `json:"format"`
		Constraints []string `json:"constraints"`
	}
	request(http.MethodGet, "/api/profile", nil, &saved)
	if profileCalls.Load() == 0 {
		t.Fatal("automatic profile update was not exercised")
	}
	if saved.Name != "Ралина" || saved.Style != "на ты" || saved.Format != "кратко" || len(saved.Stack) != 2 || len(saved.Constraints) != 1 {
		t.Fatalf("local model erased saved profile fields: %+v", saved)
	}
	data, err := os.ReadFile(filepath.Join(workDir, "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil || saved.Name != "Ралина" {
		t.Fatalf("saved profile lost name: %v", err)
	}
}
