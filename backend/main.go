package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"aiwork/backend/internal/mcpclient"
)

// defaultContextTokenLimit is a realistic context-window size to measure the
// frontend's context-usage bar against when CHAT_CONTEXT_TOKEN_LIMIT isn't
// set. It's deliberately much smaller than this gateway's actual model
// limits so the bar means something day-to-day; for a day-8 overflow demo,
// set CHAT_CONTEXT_TOKEN_LIMIT to something a short conversation can
// actually reach (e.g. 2000).
const defaultContextTokenLimit = 128000

// defaultHistoryKeepLastN is how many of the most recent messages
// sliding_window/sticky_facts resend, and how many rolling_summary keeps "as
// is" once compression is due (see Agent.compressHistoryIfDue) — matches the
// day-9 assignment's own example.
const defaultHistoryKeepLastN = 10

// defaultContextStrategy is the strategy a new (non-lab) chat starts on:
// sliding_window is the cheapest — no extra LLM call on top of the turn
// itself, unlike sticky_facts (a facts-update call) or rolling_summary (a
// summarization call once due).
const defaultContextStrategy = StrategySlidingWindow

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, relying on process environment")
	}

	apiKey := os.Getenv("LITELLM_API_KEY")
	model := os.Getenv("LITELLM_MODEL")
	if (apiKey == "") != (model == "") {
		log.Fatal("LITELLM_API_KEY and LITELLM_MODEL must both be set, or both be empty")
	}

	baseURL := os.Getenv("LITELLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://llm.effective.land"
	}

	// AGENT_USER namespaces this whole process's data tree (chats, labs,
	// projects, profile — everything main.go wires up below derives from
	// dataDir) under data/<user>/ instead of the shared default — one env
	// var picked at process start gives a fully independent agent instance
	// for a different person, without any multi-tenant UI or per-request
	// user concept. Not surfaced in the frontend; CHAT_DATA_DIR, when set
	// explicitly, still wins outright for fine-grained control.
	dataDir := os.Getenv("CHAT_DATA_DIR")
	if dataDir == "" {
		if user := os.Getenv("AGENT_USER"); user != "" {
			dataDir = filepath.Join("data", user, "sessions")
		} else {
			dataDir = "data/sessions"
		}
	}
	log.Printf("data directory: %s", dataDir)

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

	contextStrategyDefault := ContextStrategy(defaultContextStrategy)
	if v := os.Getenv("CONTEXT_STRATEGY_DEFAULT"); v != "" {
		parsed := ContextStrategy(v)
		if parsed.IsValid() {
			contextStrategyDefault = parsed
		} else {
			log.Printf("invalid CONTEXT_STRATEGY_DEFAULT %q, using default %s", v, contextStrategyDefault)
		}
	}
	log.Printf("context strategies: keep last %d message(s) raw/windowed, new chats default to %s", historyKeepLastN, contextStrategyDefault)

	var client *LiteLLMClient
	if apiKey != "" {
		client = NewLiteLLMClient(baseURL, apiKey, model)
	}
	ollamaChatURL := os.Getenv("OLLAMA_CHAT_URL")
	if ollamaChatURL == "" {
		ollamaChatURL = "http://localhost:11434"
	}
	var localURLErr error
	ollamaChatURL, localURLErr = localOllamaURL(ollamaChatURL)
	if localURLErr != nil {
		log.Fatal(localURLErr)
	}
	ollamaChatModel := os.Getenv("OLLAMA_CHAT_MODEL")
	if ollamaChatModel == "" {
		ollamaChatModel = "qwen2.5:3b"
	}
	if strings.HasSuffix(ollamaChatModel, ":cloud") {
		log.Fatal("OLLAMA_CHAT_MODEL must be a locally installed model")
	}
	ollamaDeployment := os.Getenv("OLLAMA_CHAT_DEPLOYMENT")
	if ollamaDeployment != "" && ollamaDeployment != "local" && ollamaDeployment != "private_vps" {
		log.Fatal("OLLAMA_CHAT_DEPLOYMENT must be local or private_vps")
	}
	localClient := NewLiteLLMClient(ollamaChatURL, "ollama", ollamaChatModel)
	// Labs and projects get their own sibling directories rather than
	// sharing dataDir: ChatStore.LoadAll globs every *.json file in its
	// directory as a chat, so a labs/projects file sitting next to it would
	// collide.
	labsDir := filepath.Join(filepath.Dir(dataDir), "labs")
	projectsDir := filepath.Join(filepath.Dir(dataDir), "projects")
	profilePath := filepath.Join(filepath.Dir(dataDir), "profile.json")
	agent := NewAgent(client, NewChatStore(dataDir), NewLabStore(labsDir), NewProjectStore(projectsDir), NewProfileStore(profilePath), contextTokenLimit, historyKeepLastN, contextStrategyDefault)
	agent.localClient = localClient
	agent.localConfig = localLLMConfigFromEnv()
	log.Printf("local chat: model=%s temperature=%.2f max_tokens=%d context_budget=%d", ollamaChatModel, agent.localConfig.Temperature, agent.localConfig.MaxTokens, agent.localConfig.ContextWindow)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/llm/providers", providerStatusHandler(client != nil, ollamaDeployment == "private_vps", ollamaChatURL, ollamaChatModel))
	mux.HandleFunc("GET /api/llm/performance", localModelPerformanceHandler(ollamaChatURL, ollamaChatModel, agent.localConfig))
	mux.HandleFunc("/api/estimate", cloudOnly(client, estimateHandler(client)))
	mux.HandleFunc("/api/compare", cloudOnly(client, compareHandler(client)))
	mux.HandleFunc("/api/compare/controlled", cloudOnly(client, compareControlledHandler(client)))
	mux.HandleFunc("/api/reasoning", cloudOnly(client, reasoningHandler(client)))
	mux.HandleFunc("/api/temperature", cloudOnly(client, temperatureHandler(client)))
	mux.HandleFunc("/api/models", cloudOnly(client, modelsHandler(client)))
	mux.HandleFunc("GET /api/agent/chats", listChatsHandler(agent))
	mux.HandleFunc("POST /api/agent/chats", createChatHandler(agent))
	mux.HandleFunc("GET /api/agent/chats/{id}", getChatHandler(agent))
	mux.HandleFunc("DELETE /api/agent/chats/{id}", deleteChatHandler(agent))
	mux.HandleFunc("PATCH /api/agent/chats/{id}", renameChatHandler(agent))
	mux.HandleFunc("POST /api/agent/chats/{id}/messages", postAgentMessageHandler(agent))
	mux.HandleFunc("POST /api/agent/chats/{id}/messages/stream", streamAgentMessageHandler(agent))
	mux.HandleFunc("PATCH /api/agent/chats/{id}/strategy", setStrategyHandler(agent))
	mux.HandleFunc("PATCH /api/agent/chats/{id}/rag", setRagEnabledHandler(agent))
	mux.HandleFunc("PATCH /api/agent/chats/{id}/provider", setProviderHandler(agent))
	mux.HandleFunc("POST /api/agent/chats/{id}/compress", compressChatHandler(agent))
	mux.HandleFunc("POST /api/agent/chats/{id}/checkpoints", createCheckpointHandler(agent))
	mux.HandleFunc("POST /api/agent/chats/{id}/branches", createBranchHandler(agent))
	mux.HandleFunc("PATCH /api/agent/chats/{id}/active-branch", setActiveBranchHandler(agent))
	mux.HandleFunc("POST /api/labs", cloudOnly(client, createLabHandler(agent)))
	mux.HandleFunc("POST /api/labs/{id}/analyze", cloudOnly(client, analyzeLabHandler(agent)))
	mux.HandleFunc("DELETE /api/labs/{id}", deleteLabHandler(agent))
	mux.HandleFunc("GET /api/projects", listProjectsHandler(agent))
	mux.HandleFunc("POST /api/projects", createProjectHandler(agent))
	mux.HandleFunc("GET /api/projects/{id}", getProjectHandler(agent))
	mux.HandleFunc("DELETE /api/projects/{id}", deleteProjectHandler(agent))
	mux.HandleFunc("PATCH /api/projects/{id}/memory", updateProjectMemoryHandler(agent))
	mux.HandleFunc("PATCH /api/projects/{id}/invariants", updateProjectInvariantsHandler(agent))
	mux.HandleFunc("PATCH /api/agent/chats/{id}/task", updateChatTaskHandler(agent))
	mux.HandleFunc("PATCH /api/agent/chats/{id}/task-state", updateTaskStateHandler(agent))
	mux.HandleFunc("GET /api/profile", getProfileHandler(agent))
	mux.HandleFunc("PATCH /api/profile", updateProfileHandler(agent))

	// Day 17: the product's own GitHub Activity MCP server. It's listed on
	// «Источники» next to the public one, and the chat agent holds a
	// long-lived session to it for tool calls.
	activityServer := githubActivityServerConfig()
	activityConn := mcpclient.NewConn(activityServer)
	defer activityConn.Close()

	// Day 18: the Worklog MCP server stores collected activity in SQLite
	// next to the rest of this instance's data. One session to it is shared
	// by the collector and the chat (its calls take milliseconds); the
	// collector gets its own GitHub session, so a long backfill never
	// blocks a chat turn's live GitHub call.
	worklogPath, err := filepath.Abs(filepath.Join(filepath.Dir(dataDir), "worklog.db"))
	if err != nil {
		log.Fatalf("worklog path: %v", err)
	}
	if v := os.Getenv("WORKLOG_DB"); v != "" {
		worklogPath = v
	}
	log.Printf("worklog database: %s", worklogPath)
	worklogServer := worklogServerConfig(worklogPath)
	worklogConn := mcpclient.NewConn(worklogServer)
	defer worklogConn.Close()
	agent.AddToolSource(worklogConn, "get_activity_digest", "list_events", "get_analytics", "get_sessions", "get_day_timeline", "get_repo_solidtime_projects")
	agent.AddToolSource(activityConn)

	// Day 20: the Calendar MCP server, over the user's own CalDAV account.
	// Only registered once both credentials are filled in backend/.env —
	// see calendarConfigured's doc for why a half-configured server isn't
	// even started.
	var calendarConn *mcpclient.Conn
	calendarServer := calendarServerConfig()
	registryServers := []mcpclient.ServerConfig{activityServer, worklogServer}
	if calendarConfigured() {
		calendarConn = mcpclient.NewConn(calendarServer)
		defer calendarConn.Close()
		agent.AddToolSource(calendarConn)
		registryServers = append(registryServers, calendarServer)
	} else {
		log.Printf("calendar: CALDAV_USERNAME/CALDAV_APP_PASSWORD not set, calendar server disabled")
	}

	// Day 32: the product's own Solidtime MCP server — one self-hosted
	// instance's own projects and already-tracked time entries, read-only.
	// Same optional-until-configured pattern as calendar.
	var solidtimeConn *mcpclient.Conn
	solidtimeServer := solidtimeServerConfig()
	if solidtimeConfigured() {
		solidtimeConn = mcpclient.NewConn(solidtimeServer)
		defer solidtimeConn.Close()
		// Keep the write tool outside model-routed chat turns. Only the
		// user-confirmed timesheet service may invoke it.
		agent.AddToolSource(solidtimeConn, "list_projects", "list_time_entries", "list_tasks")
		registryServers = append(registryServers, solidtimeServer)
	} else {
		log.Printf("solidtime: SOLIDTIME_BASE_URL/SOLIDTIME_API_TOKEN not set, solidtime server disabled")
	}

	collectorGitHub := mcpclient.NewConn(activityServer)
	defer collectorGitHub.Close()
	var collectorCalendar *mcpclient.Conn
	if calendarConfigured() {
		collectorCalendar = mcpclient.NewConn(calendarServer)
		defer collectorCalendar.Close()
	}
	collector := NewCollector(collectorGitHub, worklogConn, collectorCalendar, collectInterval(), filepath.Join(filepath.Dir(dataDir), "collector_runs.json"))
	collector.Start(context.Background())
	mux.HandleFunc("GET /api/activity/status", activityStatusHandler(collector))
	mux.HandleFunc("POST /api/activity/collect", activityCollectHandler(collector))
	mux.HandleFunc("GET /api/activity/digest", activityDigestHandler(worklogConn))
	mux.HandleFunc("GET /api/activity/events", activityEventsHandler(worklogConn))

	// Day 19: Analytics screen — sessions/KPIs/charts and the repo->project
	// mapping, both served straight from the Worklog MCP server.
	mux.HandleFunc("GET /api/analytics", getAnalyticsHandler(worklogConn))
	mux.HandleFunc("GET /api/analytics/repos", listRepoProjectsHandler(worklogConn))
	mux.HandleFunc("PATCH /api/analytics/repos/{repo...}", setRepoProjectHandler(worklogConn))
	mux.HandleFunc("GET /api/analytics/timeline", getDayTimelineHandler(worklogConn))

	// Day 32: the repo -> Solidtime project mapping (explicit, never
	// guessed by matching names — see cmd/mcp-worklog/solidtime_mapping.go)
	// and the timesheet audit itself, over that mapping plus Solidtime's
	// own already-tracked entries when configured.
	mux.HandleFunc("GET /api/solidtime/repo-projects", listRepoSolidtimeProjectsHandler(worklogConn))
	mux.HandleFunc("PATCH /api/solidtime/repo-projects/{repo...}", setRepoSolidtimeProjectHandler(worklogConn))
	mux.HandleFunc("GET /api/solidtime/projects", listSolidtimeProjectsHandler(solidtimeConn))
	mux.HandleFunc("GET /api/solidtime/tasks", listSolidtimeTasksHandler(solidtimeConn))
	mux.HandleFunc("GET /api/timesheet/audit", timesheetAuditHandler(worklogConn, solidtimeConn))

	// Reports live beside this instance's sessions, never in the repository.
	reports := &ReportService{worklog: worklogConn, solidtime: solidtimeConn, dir: filepath.Join(filepath.Dir(dataDir), "reports")}
	agent.reports = reports
	mux.HandleFunc("POST /api/reports/weekly", reportGenerateHandler(reports))
	mux.HandleFunc("POST /api/reports/generate", reportGenerateHandler(reports))
	mux.HandleFunc("GET /api/reports/{name}", reportDownloadHandler(reports))

	// Day 20: the weekly summary — this week vs last, facts only, written by
	// the model in the user's profile tone. Generated on demand from the
	// Analytics card, and once more automatically every Friday evening.
	weeklySummaries := NewWeeklySummaryStore(filepath.Join(filepath.Dir(dataDir), "weekly_summary.json"))
	weeklyGen := &WeeklySummaryGenerator{client: client, worklog: worklogConn, profile: agent.profileStore, store: weeklySummaries}
	mux.HandleFunc("GET /api/analytics/weekly-summary", getWeeklySummaryHandler(weeklySummaries))
	mux.HandleFunc("POST /api/analytics/weekly-summary", cloudOnly(client, generateWeeklySummaryHandler(weeklyGen)))
	if client != nil {
		startWeeklySummarySchedule(context.Background(), weeklyGen)
	}

	mcpRegistry := NewMCPRegistry(append(registryServers, mcpclient.DefaultServers()...))
	mux.HandleFunc("GET /api/mcp/servers", listMCPServersHandler(mcpRegistry))
	mux.HandleFunc("POST /api/mcp/servers/{id}/connect", connectMCPServerHandler(mcpRegistry))

	// Day 21: RAG index over the chat agent's own historical task-estimation
	// sessions. Embeddings come from local Ollama; the selected provider now
	// handles rewrite, rerank, and answer generation for standalone RAG.
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	ollamaEmbedModel := os.Getenv("OLLAMA_EMBED_MODEL")
	if ollamaEmbedModel == "" {
		log.Printf("OLLAMA_EMBED_MODEL not set — «Похожие задачи» reindex will fail until it is (e.g. \"ollama pull nomic-embed-text\")")
	}
	ollamaClient := NewOllamaClient(ollamaURL, ollamaEmbedModel)
	ragStore := NewRagStore(filepath.Join(filepath.Dir(dataDir), "rag_index.json"))
	// Day 25: the main chat grounds its own turns in this same index — see
	// rag_chat.go for why this is a direct Go wire-up, not another MCP tool
	// source like AddToolSource above.
	agent.AddRagSource(ragStore, ollamaClient)
	mux.HandleFunc("POST /api/rag/reindex", reindexHandler(agent, ragStore, ollamaClient, ollamaEmbedModel))
	mux.HandleFunc("GET /api/rag/index", getIndexHandler(ragStore))
	mux.HandleFunc("GET /api/rag/chunks", chunksHandler(ragStore))

	// Day 22: question -> retrieval -> LLM, both modes, plus the 10 control
	// questions run through both for a real quality comparison.
	ragEvalPath := filepath.Join(filepath.Dir(dataDir), "rag_eval.json")
	mux.HandleFunc("POST /api/rag/query", queryHandler(agent, ollamaClient, ragStore))
	mux.HandleFunc("POST /api/rag/compare/providers", compareProvidersHandler(agent, ollamaClient, ragStore))
	mux.HandleFunc("GET /api/rag/eval", getEvalQuestionsHandler(ragEvalPath))
	mux.HandleFunc("POST /api/rag/eval/run", evalRunHandler(agent, ollamaClient, ragStore, ragEvalPath))
	mux.HandleFunc("POST /api/rag/eval/run/stream", evalRunStreamHandler(agent, ollamaClient, ragStore, ragEvalPath))
	mux.HandleFunc("POST /api/rag/eval/retrieval", evalRetrievalHandler(ollamaClient, ragStore, ragEvalPath))

	// Day 31: a separate, much smaller RAG index over this project's own
	// README+docs grounds the /help chat command (agent_commands.go).
	// PROJECT_ROOT defaults to the parent of backend/ (how the backend is
	// documented to run — "cd backend; go run ." — README.md). Built once,
	// synchronously, at startup: the corpus is static and small enough that
	// a reindex endpoint, like the chat-history RAG has, isn't needed yet.
	projectRoot := os.Getenv("PROJECT_ROOT")
	if projectRoot == "" {
		projectRoot = ".."
	}
	docsRagStore := NewRagStore(filepath.Join(filepath.Dir(dataDir), "docs_rag_index.json"))
	agent.AddDocsRagSource(docsRagStore, ollamaClient)
	mux.HandleFunc("GET /api/docs-rag/index", getIndexHandler(docsRagStore))
	if ollamaEmbedModel == "" {
		log.Printf("OLLAMA_EMBED_MODEL not set — /help will answer without project-docs grounding until it is")
	} else if docsIdx, err := BuildDocsIndex(context.Background(), projectRoot, ollamaClient, ollamaEmbedModel); err != nil {
		log.Printf("docs rag: failed to build project-docs index, /help will answer without grounding until it's rebuilt: %v", err)
	} else if err := docsRagStore.Save(docsIdx); err != nil {
		log.Printf("docs rag: failed to save project-docs index: %v", err)
	} else {
		log.Printf("docs rag: indexed %d project doc(s) into %d chunk(s)", docsIdx.SourceCount, len(docsIdx.Chunks))
	}

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

// Unwrap lets http.ResponseController reach the underlying writer — the
// streaming endpoint needs its Flush.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
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
