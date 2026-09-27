package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A chat turn with MCP tool calls takes tens of seconds (model → tool →
// model, then the memory side-calls). The streaming endpoint below reports
// the turn's progress as it happens, as server-sent events, so the UI can
// show which tool is being called right now instead of a bare "…":
//
//	routing            {round}                  the tool-routing step is deciding (see completeWithTools)
//	tool_call_started  {id, server, server_name, tool, arguments}
//	tool_call_finished {id, record}             record is the ToolCallRecord stored on the message
//	answering          {}                       the answering call started
//	answer             AgentReply               the reply is ready; memory updates still running
//	done               AgentReply               final result, same shape as the plain endpoint's
//	error              {error}                  Russian message, same as the plain endpoint's
//
// Progress is reported through the request context, so the agent code only
// calls emitTurnEvent and never knows whether anyone is listening.

type turnEventsKey struct{}

// turnEventFunc receives one progress event. data must not be mutated after
// the call — the SSE writer marshals it synchronously.
type turnEventFunc func(event string, data any)

func withTurnEvents(ctx context.Context, fn turnEventFunc) context.Context {
	return context.WithValue(ctx, turnEventsKey{}, fn)
}

// emitTurnEvent reports progress if the caller asked for it; a no-op
// otherwise (the plain endpoint, lab fan-out, tests).
func emitTurnEvent(ctx context.Context, event string, data any) {
	if fn, ok := ctx.Value(turnEventsKey{}).(turnEventFunc); ok {
		fn(event, data)
	}
}

type toolCallStartedEvent struct {
	ID         string          `json:"id"`
	Server     string          `json:"server"`
	ServerName string          `json:"server_name"`
	Tool       string          `json:"tool"`
	Arguments  json.RawMessage `json:"arguments"`
}

type toolCallFinishedEvent struct {
	ID     string         `json:"id"`
	Record ToolCallRecord `json:"record"`
}

// sseWriter writes events to one streaming response. Writes are serialized:
// progress may be reported from more than one goroutine.
type sseWriter struct {
	mu  sync.Mutex
	w   http.ResponseWriter
	rc  *http.ResponseController
	err error
}

func (s *sseWriter) send(event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		log.Printf("stream: cannot encode %s event: %v", event, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return // client went away; the turn itself still completes and is saved
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		s.err = err
		return
	}
	s.err = s.rc.Flush()
}

// streamAgentMessageHandler is postAgentMessageHandler with progress: same
// request body, same final AgentReply (as the "done" event), same error
// messages (as the "error" event). Validation failures happen before the
// stream starts and come back as ordinary JSON errors.
func streamAgentMessageHandler(agent *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chatID := r.PathValue("id")

		var req agentMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		message := strings.TrimSpace(req.Message)
		if message == "" {
			writeError(w, http.StatusBadRequest, "сообщение не может быть пустым")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		stream := &sseWriter{w: w, rc: http.NewResponseController(w)}
		stream.err = stream.rc.Flush()

		// Same 120s budget as the plain endpoint. Deliberately not derived
		// from r.Context(): if the browser tab closes mid-turn, the turn
		// still finishes and is saved — it simply stops being reported.
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		ctx = withTurnEvents(ctx, stream.send)

		reply, err := agent.PostChatMessage(ctx, chatID, message, req.Interview)
		if err != nil {
			log.Printf("agent message (stream) failed: %v", err)
			_, message := agentErrorResponse(err)
			stream.send("error", errorResponse{Error: message})
			return
		}
		stream.send("done", reply)
	}
}
