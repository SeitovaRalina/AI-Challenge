package mcpclient

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Conn is a long-lived session to one MCP server, for callers that invoke
// tools repeatedly (the chat agent) — unlike ListTools, which opens and
// closes a session per call. It connects lazily on first use and, if a call
// fails because the session died (e.g. the stdio subprocess exited),
// reconnects once and retries.
type Conn struct {
	cfg ServerConfig

	mu      sync.Mutex
	session *mcp.ClientSession
	tools   []ToolInfo
}

func NewConn(cfg ServerConfig) *Conn {
	return &Conn{cfg: cfg}
}

func (c *Conn) Config() ServerConfig { return c.cfg }

// CallResult is one tools/call outcome. IsError is a tool-level failure
// reported by the server (bad arguments, upstream API error) — the call
// itself worked, and Text explains what went wrong.
type CallResult struct {
	Text       string          `json:"text"`
	Structured json.RawMessage `json:"structured,omitempty"`
	IsError    bool            `json:"is_error"`
	DurationMs int64           `json:"duration_ms"`
}

// ensure returns the live session, connecting first if there is none.
// Callers must hold c.mu.
func (c *Conn) ensure(ctx context.Context) (*mcp.ClientSession, error) {
	if c.session != nil {
		return c.session, nil
	}
	cctx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	defer cancel()
	session, rt, err := connect(cctx, c.cfg)
	if err != nil {
		return nil, err
	}
	res, err := describe(cctx, session, rt)
	if err != nil {
		session.Close()
		return nil, err
	}
	c.session = session
	c.tools = res.Tools
	return session, nil
}

// reset drops a dead session so the next call reconnects. Callers must
// hold c.mu.
func (c *Conn) reset() {
	if c.session != nil {
		c.session.Close()
	}
	c.session = nil
	c.tools = nil
}

// Tools returns the server's tools, connecting first if needed.
func (c *Conn) Tools(ctx context.Context) ([]ToolInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.ensure(ctx); err != nil {
		return nil, err
	}
	return c.tools, nil
}

// Call invokes a tool with JSON-object arguments.
func (c *Conn) Call(ctx context.Context, name string, args map[string]any) (*CallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	started := time.Now()
	var res *mcp.CallToolResult
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		var session *mcp.ClientSession
		session, err = c.ensure(ctx)
		if err != nil {
			return nil, err
		}
		res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err == nil || ctx.Err() != nil {
			break
		}
		// Transport-level failure: the session is likely dead — reconnect
		// once and retry.
		c.reset()
	}
	if err != nil {
		return nil, err
	}

	out := &CallResult{IsError: res.IsError, DurationMs: time.Since(started).Milliseconds()}
	var texts []string
	for _, content := range res.Content {
		if t, ok := content.(*mcp.TextContent); ok {
			texts = append(texts, t.Text)
		}
	}
	out.Text = strings.Join(texts, "\n")
	if res.StructuredContent != nil {
		if raw, err := json.Marshal(res.StructuredContent); err == nil {
			out.Structured = raw
		}
	}
	return out, nil
}

// Close ends the session (and, for stdio, lets the subprocess exit).
func (c *Conn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reset()
}
