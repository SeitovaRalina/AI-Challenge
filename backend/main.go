package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// defaultContextTokenLimit is a realistic context-window size to measure the
// frontend's context-usage bar against when CHAT_CONTEXT_TOKEN_LIMIT isn't
// set. It's deliberately much smaller than this gateway's actual model
// limits so the bar means something day-to-day; for a day-8 overflow demo,
// set CHAT_CONTEXT_TOKEN_LIMIT to something a short conversation can
// actually reach (e.g. 2000).
const defaultContextTokenLimit = 128000

// defaultHistoryKeepLastN is how many of the most recent messages a chat
// keeps "as is" once history compression is due (see
// Agent.compressHistoryIfDue) — matches the day-9 assignment's own example.
const defaultHistoryKeepLastN = 10

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, relying on process environment")
	}

	apiKey := os.Getenv("LITELLM_API_KEY")
	model := os.Getenv("LITELLM_MODEL")
	if apiKey == "" || model == "" {
		log.Fatal("LITELLM_API_KEY and LITELLM_MODEL must be set")
	}

	baseURL := os.Getenv("LITELLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://llm.effective.land"
	}

	dataDir := os.Getenv("CHAT_DATA_DIR")
	if dataDir == "" {
		dataDir = "data/sessions"
	}

	contextTokenLimit := defaultContextTokenLimit
	if v := os.Getenv("CHAT_CONTEXT_TOKEN_LIMIT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			contextTokenLimit = parsed
		} else {
			log.Printf("invalid CHAT_CONTEXT_TOKEN_LIMIT %q, using default %d", v, contextTokenLimit)
		}
	}
	log.Printf("chat context token limit: %d", contextTokenLimit)

	historyKeepLastN := defaultHistoryKeepLastN
	if v := os.Getenv("HISTORY_KEEP_LAST_N"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			historyKeepLastN = parsed
		} else {
			log.Printf("invalid HISTORY_KEEP_LAST_N %q, using default %d", v, historyKeepLastN)
		}
	}

	historyCompressionDefault := true
	if v := os.Getenv("HISTORY_COMPRESSION_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			historyCompressionDefault = parsed
		} else {
			log.Printf("invalid HISTORY_COMPRESSION_ENABLED %q, using default %t", v, historyCompressionDefault)
		}
	}
	log.Printf("history compression: keep last %d message(s) raw, default %t for new chats", historyKeepLastN, historyCompressionDefault)

	client := NewLiteLLMClient(baseURL, apiKey, model)
	agent := NewAgent(client, NewChatStore(dataDir), contextTokenLimit, historyKeepLastN, historyCompressionDefault)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/estimate", estimateHandler(client))
	mux.HandleFunc("/api/compare", compareHandler(client))
	mux.HandleFunc("/api/compare/controlled", compareControlledHandler(client))
	mux.HandleFunc("/api/reasoning", reasoningHandler(client))
	mux.HandleFunc("/api/temperature", temperatureHandler(client))
	mux.HandleFunc("/api/models", modelsHandler(client))
	mux.HandleFunc("GET /api/agent/chats", listChatsHandler(agent))
	mux.HandleFunc("POST /api/agent/chats", createChatHandler(agent))
	mux.HandleFunc("GET /api/agent/chats/{id}", getChatHandler(agent))
	mux.HandleFunc("DELETE /api/agent/chats/{id}", deleteChatHandler(agent))
	mux.HandleFunc("PATCH /api/agent/chats/{id}", renameChatHandler(agent))
	mux.HandleFunc("POST /api/agent/chats/{id}/messages", postAgentMessageHandler(agent))
	mux.HandleFunc("POST /api/agent/chats/{id}/compress", compressChatHandler(agent))
	mux.HandleFunc("PATCH /api/agent/chats/{id}/compression", setCompressionHandler(agent))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("backend listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, withCORS(withRequestLog(mux))))
}

// withRequestLog logs every request's method, path, resulting status, and
// duration, so backend behavior can be traced from the terminal without
// attaching a debugger.
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

// statusWriter captures the status code a handler writes, since
// http.ResponseWriter doesn't expose it after the fact.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// withCORS allows the local Vite dev server to call the API directly.
// The frontend never receives or forwards the LiteLLM API key.
func withCORS(next http.Handler) http.Handler {
	allowedOrigin := os.Getenv("CORS_ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:5173"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
