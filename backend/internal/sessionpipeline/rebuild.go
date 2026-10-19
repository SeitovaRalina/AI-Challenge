package sessionpipeline

import (
	"encoding/json"
	"fmt"
)

// Call describes a decoded MCP operation without coupling the session rules to a transport.
type Call func(tool string, args map[string]any, detail string, out any, summarize func() string) error

type Result struct {
	EventsIn    int
	SessionsOut int
	Replaced    int
}

// Rebuild replaces stored sessions only after the full event history was composed.
func Rebuild(call Call) (Result, error) {
	var all struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := call("list_events", map[string]any{"from": "1970-01-01", "all": true}, "вся история", &all, func() string {
		return fmt.Sprintf("%d событий", len(all.Events))
	}); err != nil {
		return Result{}, err
	}

	var built struct {
		Sessions    []json.RawMessage `json:"sessions"`
		SessionsOut int               `json:"sessions_out"`
	}
	if err := call("build_sessions", map[string]any{"events": all.Events}, fmt.Sprintf("%d событий", len(all.Events)), &built, func() string {
		return fmt.Sprintf("%d сессий", built.SessionsOut)
	}); err != nil {
		return Result{}, err
	}

	var saved struct {
		Saved    int `json:"saved"`
		Replaced int `json:"replaced"`
	}
	if err := call("save_sessions", map[string]any{"sessions": built.Sessions}, fmt.Sprintf("%d сессий", len(built.Sessions)), &saved, func() string {
		return fmt.Sprintf("сохранено %d (было %d)", saved.Saved, saved.Replaced)
	}); err != nil {
		return Result{}, err
	}
	return Result{EventsIn: len(all.Events), SessionsOut: saved.Saved, Replaced: saved.Replaced}, nil
}
