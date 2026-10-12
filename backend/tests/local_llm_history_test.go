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
	"strings"
	"testing"
	"time"
)

// The local model may misread a historical-hours question as a new estimate.
// Retrieved numerical evidence must still determine the visible answer and
// citation, without reading any of the user's real conversations.
func TestLocalHistoricalHoursUsesRetrievedEstimate(t *testing.T) {
	const source = "Предварительная оценка: Экспорт списка заказов в CSV — 4–7 ч."
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]string{"name": "local-model"}}})
		case "/api/embed":
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0}}})
		case "/v1/chat/completions":
			writeCompletion(t, w, `{"reply":"estimate=null","estimate":{"summary":"Wrong new estimate","category":"feature","complexity":"low","estimated_hours_min":9,"estimated_hours_max":12,"risks":[],"assumptions":[],"subtasks":[]},"invariant_conflict":[],"citations":[]}`)
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
	bin := filepath.Join(workDir, "backend")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = backendDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := map[string]any{"source_count": 1, "embed_model": "test-embed", "chunks": []any{map[string]any{
		"id": "c1", "session_id": "synthetic", "title": "CSV export", "section": "estimate", "strategy": "structural", "text": source, "embedding": []float32{1, 0},
	}}}
	data, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(workDir, "rag_index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	cmd := exec.Command(bin)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PORT="+strconv.Itoa(port), "CHAT_DATA_DIR="+filepath.Join(workDir, "sessions"),
		"OLLAMA_CHAT_URL="+local.URL, "OLLAMA_CHAT_MODEL=local-model", "OLLAMA_URL="+local.URL,
		"OLLAMA_EMBED_MODEL=test-embed", "LITELLM_API_KEY=", "LITELLM_MODEL=",
		"GITHUB_TOKEN=", "CALDAV_USERNAME=", "CALDAV_APP_PASSWORD=", "COLLECT_INTERVAL=off", "MCP_WORKLOG_COMMAND=nonexistent-worklog-command",
	)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		resp, err := http.Get(base + "/api/llm/providers")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
	}
	post := func(path string, payload any, out any) {
		t.Helper()
		body, _ := json.Marshal(payload)
		resp, err := http.Post(base+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			t.Fatalf("POST %s: HTTP %d, logs:\n%s", path, resp.StatusCode, logs.String())
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	var chat struct {
		ID string `json:"id"`
	}
	post("/api/agent/chats", map[string]string{"provider": "ollama"}, &chat)
	req, _ := http.NewRequest(http.MethodPatch, base+"/api/agent/chats/"+chat.ID+"/rag", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("enable RAG: HTTP %d", resp.StatusCode)
	}
	var answer struct {
		Reply     string          `json:"reply"`
		Estimate  json.RawMessage `json:"estimate"`
		Citations []struct {
			Text     string `json:"text"`
			Verified bool   `json:"verified"`
		} `json:"citations"`
	}
	post("/api/agent/chats/"+chat.ID+"/messages", map[string]string{"message": "Сколько часов заняла похожая задача экспорта CSV?"}, &answer)
	if !strings.Contains(answer.Reply, "4–7 ч") || !strings.Contains(answer.Reply, "Фактически затраченное время") || string(answer.Estimate) != "null" || len(answer.Citations) != 1 || !answer.Citations[0].Verified || answer.Citations[0].Text != source {
		t.Fatalf("historical answer not grounded: %+v", answer)
	}
}
