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
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real HTTP server with a tiny synthetic index. The user's
// conversations and index are never read or copied.
func TestRagProviderComparisonSharesRetrievalAndWorksOffline(t *testing.T) {
	var embeds, localCalls, cloudCalls atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]string{"name": "local-model"}}})
		case "/api/embed":
			embeds.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0}}})
		case "/v1/chat/completions":
			localCalls.Add(1)
			writeCompletion(t, w, `{"answer":"Локально: два часа.","citations":[{"chunk_id":"c1","text":"два часа"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer local.Close()
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudCalls.Add(1)
		writeCompletion(t, w, `{"answer":"Облако: два часа.","citations":[{"chunk_id":"c1","text":"два часа"}]}`)
	}))
	defer cloud.Close()

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
		"id": "c1", "session_id": "synthetic", "title": "Synthetic task", "section": "estimate", "strategy": "structural", "text": "Экспорт занял два часа.", "embedding": []float32{1, 0},
	}}}
	data, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(workDir, "rag_index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	evalData, _ := json.Marshal([]map[string]any{{"question": "Сколько занял экспорт?", "expectation": "Два часа", "expected_sources": []string{"synthetic"}}})
	if err := os.WriteFile(filepath.Join(workDir, "rag_eval.json"), evalData, 0o644); err != nil {
		t.Fatal(err)
	}

	start := func(cloudConfigured bool) (string, func()) {
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
		if cloudConfigured {
			cmd.Env = append(cmd.Env, "LITELLM_BASE_URL="+cloud.URL, "LITELLM_API_KEY=test-only-key", "LITELLM_MODEL=cloud-model")
		}
		var logs bytes.Buffer
		cmd.Stdout, cmd.Stderr = &logs, &logs
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stop := func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			resp, err := http.Get(base + "/api/llm/providers")
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == 200 {
					return base, stop
				}
			}
		}
		stop()
		t.Fatalf("server start failed: %s", logs.String())
		return "", stop
	}
	call := func(base string, payload any, path string) (int, map[string]any) {
		body, _ := json.Marshal(payload)
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Post(base+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, result
	}
	request := map[string]any{"question": "Сколько занял экспорт?", "strategy": "structural"}
	base, stop := start(true)
	status, result := call(base, request, "/api/rag/compare/providers")
	stop()
	if status != 200 || embeds.Load() != 1 || localCalls.Load() != 1 || cloudCalls.Load() != 1 {
		t.Fatalf("comparison: status=%d embeds=%d local=%d cloud=%d result=%v", status, embeds.Load(), localCalls.Load(), cloudCalls.Load(), result)
	}
	for _, provider := range []string{"local", "cloud"} {
		generation := result[provider].(map[string]any)
		answer := generation["answer"].(map[string]any)
		citations := answer["citations"].([]any)
		if len(citations) != 1 || citations[0].(map[string]any)["verified"] != true {
			t.Fatalf("%s citation: %v", provider, citations)
		}
	}
	base, stop = start(false)
	defer stop()
	status, result = call(base, request, "/api/rag/compare/providers")
	if status != 200 || result["local"].(map[string]any)["answer"] == nil || result["cloud"].(map[string]any)["error"] == nil {
		t.Fatalf("offline comparison: status=%d result=%v", status, result)
	}
	status, result = call(base, map[string]any{"question": "Сколько занял экспорт?", "mode": "rag", "strategy": "structural", "provider": "ollama"}, "/api/rag/query")
	if status != 200 || result["answer"] == nil {
		t.Fatalf("offline RAG query: status=%d result=%v", status, result)
	}
	status, result = call(base, map[string]any{"strategy": "structural", "provider": "ollama"}, "/api/rag/eval/run")
	if status != 200 || len(result["results"].([]any)) != 1 {
		t.Fatalf("offline RAG eval: status=%d result=%v", status, result)
	}
}
