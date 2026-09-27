// Package mcpclient connects to an MCP server over streamable HTTP, runs the
// initialize handshake, and lists the tools it exposes. It is shared by the
// backend's /api/mcp endpoints and the cmd/mcp-tools CLI, so both report
// exactly the same result for the same server.
package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ConnectTimeout bounds the whole connect → initialize → tools/list round
// trip, so a hung server can't block an HTTP handler indefinitely.
const ConnectTimeout = 15 * time.Second

// ServerConfig describes one remote MCP server. The token itself is never
// stored here — only the name of the env var holding it — so a config can
// be serialized to the frontend without leaking a secret.
type ServerConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	TokenEnv string `json:"token_env,omitempty"`
	// ReadOnly asks the server to expose only read-only tools. GitHub's
	// remote MCP honors this via the X-MCP-Readonly header; the product
	// only ever reads work activity, so there's no reason to see write
	// tools at all.
	ReadOnly bool `json:"read_only"`
}

// Token returns the server's bearer token from the environment, or "" when
// the server needs none or it isn't set.
func (c ServerConfig) Token() string {
	if c.TokenEnv == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(c.TokenEnv))
}

// ToolParam is one input parameter of a tool, flattened out of its JSON
// schema for display.
type ToolParam struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// ToolInfo is one tool as returned by tools/list.
type ToolInfo struct {
	Name        string      `json:"name"`
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	ReadOnly    bool        `json:"read_only"`
	Params      []ToolParam `json:"params"`
	InputSchema any         `json:"input_schema,omitempty"`
}

// Result is what a successful connect + tools/list produced.
type Result struct {
	ProtocolVersion string     `json:"protocol_version"`
	ServerName      string     `json:"server_name"`
	ServerVersion   string     `json:"server_version,omitempty"`
	Instructions    string     `json:"instructions,omitempty"`
	Tools           []ToolInfo `json:"tools"`
	DurationMs      int64      `json:"duration_ms"`
}

// ErrorKind classifies a failed connection so callers can explain it to
// the user instead of surfacing a raw transport error.
type ErrorKind string

const (
	ErrNoToken      ErrorKind = "no_token"
	ErrUnauthorized ErrorKind = "unauthorized"
	ErrUnreachable  ErrorKind = "unreachable"
	ErrTimeout      ErrorKind = "timeout"
	ErrProtocol     ErrorKind = "protocol"
)

// Error is a classified connection failure.
type Error struct {
	Kind   ErrorKind
	Status int // last HTTP status seen from the server, 0 if none
	Err    error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return string(e.Kind)
	}
	return fmt.Sprintf("%s: %v", e.Kind, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// headerTransport injects auth/read-only headers into every request of the
// session and remembers the last non-2xx status, since the SDK reports an
// HTTP 401 only as an opaque transport error.
type headerTransport struct {
	base     http.RoundTripper
	headers  map[string]string
	mu       sync.Mutex
	lastFail int
	// authRejected is set when the server rejects the token. GitHub answers
	// a malformed or invalid token with 400, not 401, but always marks it
	// with a WWW-Authenticate: Bearer error="invalid_token" header.
	authRejected bool
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.base.RoundTrip(req)
	if err == nil && resp.StatusCode >= 400 {
		t.mu.Lock()
		t.lastFail = resp.StatusCode
		if strings.Contains(resp.Header.Get("WWW-Authenticate"), "invalid_token") {
			t.authRejected = true
		}
		t.mu.Unlock()
	}
	return resp, err
}

func (t *headerTransport) failure() (status int, authRejected bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastFail, t.authRejected
}

// ListTools opens a session to cfg's server, performs the initialize
// handshake, collects every page of tools/list, and closes the session.
// A failure is always returned as *Error.
func ListTools(ctx context.Context, cfg ServerConfig) (*Result, error) {
	token := cfg.Token()
	if cfg.TokenEnv != "" && token == "" {
		return nil, &Error{Kind: ErrNoToken, Err: fmt.Errorf("%s is not set", cfg.TokenEnv)}
	}

	ctx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	defer cancel()

	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if cfg.ReadOnly {
		headers["X-MCP-Readonly"] = "true"
	}
	rt := &headerTransport{base: http.DefaultTransport, headers: headers}

	transport := &mcp.StreamableClientTransport{
		Endpoint:   cfg.URL,
		HTTPClient: &http.Client{Transport: rt},
		// One request/response exchange is all this needs: no standalone
		// SSE stream for server-initiated notifications, and no retries
		// that would only delay reporting a real failure.
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "aiwork-backend", Version: "0.1.0"}, nil)

	started := time.Now()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, classify(ctx, err, rt)
	}
	defer session.Close()

	init := session.InitializeResult()
	result := &Result{Tools: []ToolInfo{}}
	if init != nil {
		result.ProtocolVersion = init.ProtocolVersion
		result.Instructions = init.Instructions
		if init.ServerInfo != nil {
			result.ServerName = init.ServerInfo.Name
			result.ServerVersion = init.ServerInfo.Version
		}
	}

	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, classify(ctx, err, rt)
		}
		result.Tools = append(result.Tools, toolInfo(tool))
	}
	result.DurationMs = time.Since(started).Milliseconds()
	return result, nil
}

func classify(ctx context.Context, err error, rt *headerTransport) *Error {
	status, authRejected := rt.failure()
	switch {
	case authRejected || status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &Error{Kind: ErrUnauthorized, Status: status, Err: err}
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		return &Error{Kind: ErrTimeout, Status: status, Err: err}
	}
	var netErr net.Error
	var dnsErr *net.DNSError
	var opErr *net.OpError
	if errors.As(err, &dnsErr) || errors.As(err, &opErr) || (errors.As(err, &netErr) && !netErr.Timeout()) || status == http.StatusNotFound {
		return &Error{Kind: ErrUnreachable, Status: status, Err: err}
	}
	return &Error{Kind: ErrProtocol, Status: status, Err: err}
}

func toolInfo(t *mcp.Tool) ToolInfo {
	info := ToolInfo{
		Name:        t.Name,
		Title:       t.Title,
		Description: t.Description,
		InputSchema: t.InputSchema,
		Params:      paramsFromSchema(t.InputSchema),
	}
	if t.Annotations != nil {
		info.ReadOnly = t.Annotations.ReadOnlyHint
		if info.Title == "" {
			info.Title = t.Annotations.Title
		}
	}
	return info
}

// paramsFromSchema flattens the top-level properties of a tool's JSON
// input schema. Tools received over the wire carry their schema as a
// decoded map[string]any.
func paramsFromSchema(schema any) []ToolParam {
	m, ok := schema.(map[string]any)
	if !ok {
		return []ToolParam{}
	}
	required := map[string]bool{}
	if req, ok := m["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	props, _ := m["properties"].(map[string]any)
	params := make([]ToolParam, 0, len(props))
	for name, raw := range props {
		p := ToolParam{Name: name, Required: required[name]}
		if prop, ok := raw.(map[string]any); ok {
			p.Type = schemaType(prop)
			p.Description, _ = prop["description"].(string)
		}
		params = append(params, p)
	}
	// Required first, then alphabetical, so the order is stable across
	// calls (map iteration isn't) and the essentials come first.
	sortParams(params)
	return params
}

func schemaType(prop map[string]any) string {
	switch t := prop["type"].(type) {
	case string:
		if t == "array" {
			if items, ok := prop["items"].(map[string]any); ok {
				if it, ok := items["type"].(string); ok {
					return it + "[]"
				}
			}
		}
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " | ")
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if variants, ok := prop[key].([]any); ok {
			parts := make([]string, 0, len(variants))
			for _, v := range variants {
				if vm, ok := v.(map[string]any); ok {
					if s := schemaType(vm); s != "" {
						parts = append(parts, s)
					}
				}
			}
			return strings.Join(parts, " | ")
		}
	}
	if _, ok := prop["enum"]; ok {
		return "enum"
	}
	return ""
}

func sortParams(params []ToolParam) {
	for i := 1; i < len(params); i++ {
		for j := i; j > 0 && lessParam(params[j], params[j-1]); j-- {
			params[j], params[j-1] = params[j-1], params[j]
		}
	}
}

func lessParam(a, b ToolParam) bool {
	if a.Required != b.Required {
		return a.Required
	}
	return a.Name < b.Name
}

// DefaultGitHubURL is GitHub's official remote MCP server.
const DefaultGitHubURL = "https://api.githubcopilot.com/mcp/"

// DefaultServers is the fixed set of MCP servers this instance knows
// about: GitHub's official remote MCP server, the one public server
// relevant to the product (work activity lives in GitHub). GITHUB_MCP_URL
// overrides its endpoint.
func DefaultServers() []ServerConfig {
	url := os.Getenv("GITHUB_MCP_URL")
	if url == "" {
		url = DefaultGitHubURL
	}
	return []ServerConfig{{
		ID:       "github",
		Name:     "GitHub MCP (официальный)",
		URL:      url,
		TokenEnv: "GITHUB_TOKEN",
		ReadOnly: true,
	}}
}
