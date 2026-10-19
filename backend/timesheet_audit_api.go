package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"aiwork/backend/internal/mcpclient"
)

// REST for day 32: the Solidtime projects list (for the repo-mapping UI,
// «Аналитика» screen) and the raw timesheet audit — a debugging/testing
// surface, same engine as /audit-timesheet (agent_commands.go) minus the
// LLM-written remarks, so it costs nothing to call and never needs a
// configured LLM provider.

func listSolidtimeProjectsHandler(solidtime *mcpclient.Conn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if solidtime == nil {
			writeError(w, http.StatusServiceUnavailable, "Solidtime не настроен")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		var out struct {
			Projects []ProjectInfoView `json:"projects"`
		}
		if err := mcpclient.CallJSON(ctx, solidtime, "list_projects", map[string]any{}, &out); err != nil {
			writeError(w, http.StatusBadGateway, "Solidtime MCP недоступен: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// ProjectInfoView mirrors cmd/mcp-solidtime's ProjectInfo JSON shape —
// redeclared here for the same reason sessionRaw is (timesheet_audit.go):
// that's a separate `main` package.
type ProjectInfoView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsArchived bool   `json:"is_archived"`
}

func auditMCPCalls(ctx context.Context, worklog, solidtime *mcpclient.Conn) (mcpCall, mcpCall) {
	callWorklog := func(tool string, args map[string]any, out any) error {
		return mcpclient.CallJSON(ctx, worklog, tool, args, out)
	}
	var callSolidtime mcpCall
	if solidtime != nil {
		callSolidtime = func(tool string, args map[string]any, out any) error {
			return mcpclient.CallJSON(ctx, solidtime, tool, args, out)
		}
	}
	return callWorklog, callSolidtime
}

// timesheetAuditHandler runs fetchAndAudit directly over conn.Call, with
// no LLM involved at all — ?days=N, defaulting to 7, same convention as
// /work-summary and /audit-timesheet.
func timesheetAuditHandler(worklog, solidtime *mcpclient.Conn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := defaultAuditTimesheetDays
		if raw := r.URL.Query().Get("days"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, "days должен быть положительным числом")
				return
			}
			days = n
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		to := time.Now()
		from := to.AddDate(0, 0, -days)

		callWorklog, callSolidtime := auditMCPCalls(ctx, worklog, solidtime)

		result, err := fetchAndAudit(from, to, solidtime != nil, callWorklog, callSolidtime)
		if err != nil {
			writeError(w, http.StatusBadGateway, "аудит не удался: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
