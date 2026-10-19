package sessionpipeline

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestRebuildOrdersCallsAndPassesComposedSessions(t *testing.T) {
	var calls []string
	result, err := Rebuild(func(tool string, args map[string]any, _ string, out any, summarize func() string) error {
		calls = append(calls, tool)
		switch tool {
		case "list_events":
			if args["all"] != true {
				t.Fatalf("expected full history: %v", args)
			}
			return json.Unmarshal([]byte(`{"events":[{"id":"event-1"}]}`), out)
		case "build_sessions":
			if len(args["events"].([]json.RawMessage)) != 1 {
				t.Fatalf("events not forwarded: %v", args)
			}
			return json.Unmarshal([]byte(`{"sessions":[{"id":"session-1"}],"sessions_out":1}`), out)
		case "save_sessions":
			if len(args["sessions"].([]json.RawMessage)) != 1 {
				t.Fatalf("sessions not forwarded: %v", args)
			}
			return json.Unmarshal([]byte(`{"saved":1,"replaced":2}`), out)
		}
		return errors.New("unexpected tool")
	})
	if err != nil || !reflect.DeepEqual(calls, []string{"list_events", "build_sessions", "save_sessions"}) || result != (Result{EventsIn: 1, SessionsOut: 1, Replaced: 2}) {
		t.Fatalf("result=%+v calls=%v err=%v", result, calls, err)
	}
}

func TestRebuildDoesNotSaveAfterBuildFailure(t *testing.T) {
	var calls []string
	_, err := Rebuild(func(tool string, _ map[string]any, _ string, out any, _ func() string) error {
		calls = append(calls, tool)
		if tool == "list_events" {
			return json.Unmarshal([]byte(`{"events":[{"id":"event-1"}]}`), out)
		}
		return errors.New("composition failed")
	})
	if err == nil || !reflect.DeepEqual(calls, []string{"list_events", "build_sessions"}) {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
}
