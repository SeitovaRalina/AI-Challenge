package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"aiwork/backend/internal/mcpclient"
)

// Day 16: the first external MCP connection. The backend only performs the
// initialize handshake and tools/list against a public MCP server and shows
// the result on the «Источники» screen — no tool is called yet.

// githubActivityServerConfig describes the product's own GitHub Activity MCP
// server (cmd/mcp-github), started as a stdio subprocess. By default it runs
// through `go run` from the backend module (cached after the first build);
// MCP_GITHUB_COMMAND overrides that with a prebuilt binary's command line.
// The subprocess inherits this process's environment, GITHUB_TOKEN and
// GITHUB_REPOS included.
func githubActivityServerConfig() mcpclient.ServerConfig {
	command := strings.Fields(os.Getenv("MCP_GITHUB_COMMAND"))
	if len(command) == 0 {
		command = []string{"go", "run", "./cmd/mcp-github"}
	}
	return mcpclient.ServerConfig{
		ID:        "github-activity",
		Name:      "GitHub Activity (свой MCP)",
		Transport: mcpclient.TransportStdio,
		Command:   strings.Join(command, " "),
		NewCommand: func() (*exec.Cmd, error) {
			cmd := exec.Command(command[0], command[1:]...)
			// Server logs go to stderr; surface them in the backend's own log.
			cmd.Stderr = os.Stderr
			return cmd, nil
		},
		Own:      true,
		TokenEnv: "GITHUB_TOKEN",
		ReadOnly: true,
	}
}

// worklogServerConfig describes the product's own Worklog MCP server
// (cmd/mcp-worklog, day 18): the SQLite store of collected work activity at
// dbPath. MCP_WORKLOG_COMMAND overrides the default `go run` the same way
// MCP_GITHUB_COMMAND does.
func worklogServerConfig(dbPath string) mcpclient.ServerConfig {
	command := strings.Fields(os.Getenv("MCP_WORKLOG_COMMAND"))
	if len(command) == 0 {
		command = []string{"go", "run", "./cmd/mcp-worklog"}
	}
	return mcpclient.ServerConfig{
		ID:        "worklog",
		Name:      "Worklog (свой MCP)",
		Transport: mcpclient.TransportStdio,
		Command:   strings.Join(command, " "),
		NewCommand: func() (*exec.Cmd, error) {
			cmd := exec.Command(command[0], command[1:]...)
			cmd.Env = append(os.Environ(), "WORKLOG_DB="+dbPath)
			cmd.Stderr = os.Stderr
			return cmd, nil
		},
		Own: true,
	}
}

// calendarServerConfig describes the product's own Calendar MCP server
// (cmd/mcp-calendar, day 20): the user's CalDAV account. Unlike GitHub and
// Worklog, this one has no fallback — until CALDAV_USERNAME and
// CALDAV_APP_PASSWORD are filled in backend/.env there is simply no
// calendar source; callers check calendarConfigured before registering it
// anywhere a missing server would otherwise be confusing.
func calendarServerConfig() mcpclient.ServerConfig {
	command := strings.Fields(os.Getenv("MCP_CALENDAR_COMMAND"))
	if len(command) == 0 {
		command = []string{"go", "run", "./cmd/mcp-calendar"}
	}
	return mcpclient.ServerConfig{
		ID:        "calendar",
		Name:      "Calendar (свой MCP)",
		Transport: mcpclient.TransportStdio,
		Command:   strings.Join(command, " "),
		NewCommand: func() (*exec.Cmd, error) {
			cmd := exec.Command(command[0], command[1:]...)
			cmd.Stderr = os.Stderr
			return cmd, nil
		},
		Own:      true,
		TokenEnv: "CALDAV_APP_PASSWORD",
		ReadOnly: true,
	}
}

// calendarConfigured is true once both CalDAV credentials are set — the
// backend registers and starts the calendar server (chat tools, collector
// step) only then, rather than spawning a subprocess doomed to fail its
// first call.
func calendarConfigured() bool {
	return strings.TrimSpace(os.Getenv("CALDAV_USERNAME")) != "" && strings.TrimSpace(os.Getenv("CALDAV_APP_PASSWORD")) != ""
}

// MCPConnection is the outcome of the most recent connect attempt to a
// server — either the tools it listed, or a classified error.
type MCPConnection struct {
	Status      string            `json:"status"` // "connected" | "error"
	CheckedAt   time.Time         `json:"checked_at"`
	Result      *mcpclient.Result `json:"result,omitempty"`
	ErrorKind   string            `json:"error_kind,omitempty"`
	ErrorStatus int               `json:"error_status,omitempty"`
	Error       string            `json:"error,omitempty"`
}

// MCPServerView is a server as the frontend sees it: its config, whether a
// token is configured (never the token itself), and the last connection.
type MCPServerView struct {
	mcpclient.ServerConfig
	TokenSet       bool           `json:"token_set"`
	LastConnection *MCPConnection `json:"last_connection,omitempty"`
}

// MCPRegistry holds the configured servers and each one's last connection
// result, in memory only: a tools list is cheap to re-fetch and goes stale
// anyway, so there's nothing worth persisting across restarts.
type MCPRegistry struct {
	servers []mcpclient.ServerConfig
	mu      sync.Mutex
	last    map[string]*MCPConnection
}

var ErrMCPServerNotFound = errors.New("mcp server not found")

func NewMCPRegistry(servers []mcpclient.ServerConfig) *MCPRegistry {
	return &MCPRegistry{servers: servers, last: map[string]*MCPConnection{}}
}

func (r *MCPRegistry) List() []MCPServerView {
	r.mu.Lock()
	defer r.mu.Unlock()
	views := make([]MCPServerView, 0, len(r.servers))
	for _, s := range r.servers {
		views = append(views, MCPServerView{ServerConfig: s, TokenSet: s.Token() != "", LastConnection: r.last[s.ID]})
	}
	return views
}

// Connect runs initialize + tools/list against the server and records the
// outcome. A server-side failure is not a Go error here: it's returned as
// an MCPConnection with Status "error", because to the caller "the server
// rejected the token" is a normal, displayable result.
func (r *MCPRegistry) Connect(ctx context.Context, id string) (MCPServerView, error) {
	var cfg *mcpclient.ServerConfig
	for i := range r.servers {
		if r.servers[i].ID == id {
			cfg = &r.servers[i]
			break
		}
	}
	if cfg == nil {
		return MCPServerView{}, ErrMCPServerNotFound
	}

	conn := &MCPConnection{CheckedAt: time.Now().UTC()}
	result, err := mcpclient.ListTools(ctx, *cfg)
	if err != nil {
		conn.Status = "error"
		conn.Error = err.Error()
		var mcpErr *mcpclient.Error
		if errors.As(err, &mcpErr) {
			conn.ErrorKind = string(mcpErr.Kind)
			conn.ErrorStatus = mcpErr.Status
		}
		log.Printf("mcp: %s connect failed: %v", cfg.ID, err)
	} else {
		conn.Status = "connected"
		conn.Result = result
		log.Printf("mcp: %s connected (%s %s, protocol %s), %d tool(s) in %dms",
			cfg.ID, result.ServerName, result.ServerVersion, result.ProtocolVersion, len(result.Tools), result.DurationMs)
	}

	r.mu.Lock()
	r.last[cfg.ID] = conn
	r.mu.Unlock()
	return MCPServerView{ServerConfig: *cfg, TokenSet: cfg.Token() != "", LastConnection: conn}, nil
}

func listMCPServersHandler(reg *MCPRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, reg.List())
	}
}

func connectMCPServerHandler(reg *MCPRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		view, err := reg.Connect(r.Context(), r.PathValue("id"))
		if errors.Is(err, ErrMCPServerNotFound) {
			writeError(w, http.StatusNotFound, "MCP-сервер не найден")
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}
