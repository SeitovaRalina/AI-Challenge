package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"aiwork/backend/internal/mcpclient"
)

// Day 32: deterministic flagging first (flagSession below) — the model
// never decides whether a session needs a human look, only how to phrase
// that in Russian, for the sessions already singled out (writeAuditRemarks).
// Same discipline as /work-summary (agent_commands.go, day 31) and
// rerankLLM's index-correlated JSON array (rag_query.go, day 23).

// AuditFlag is one objective, programmatically-detected reason a draft
// session needs a human look before being trusted as a timesheet entry.
type AuditFlag string

const (
	// FlagPossibleDuplicate: the session's time range overlaps a time
	// entry already tracked in Solidtime — syncing it as-is risks counting
	// the same work twice.
	FlagPossibleDuplicate AuditFlag = "possible_duplicate"
	// FlagUnmappedSolidtimeProject: the session's repository has no
	// explicit Solidtime project mapping yet (set_repo_solidtime_project,
	// cmd/mcp-worklog/solidtime_mapping.go) — never guessed by matching
	// names, since a repo and its Solidtime project name routinely differ.
	FlagUnmappedSolidtimeProject AuditFlag = "unmapped_solidtime_project"
	// FlagSuspiciousDuration: zero/negative, or implausibly long for one
	// contiguous block.
	FlagSuspiciousDuration AuditFlag = "suspicious_duration"
)

// maxPlausibleSessionDuration: a single work session longer than this is
// almost certainly several real blocks the lead-in/gap heuristic
// (cmd/mcp-worklog/sessions.go) merged too eagerly, not one real sitting.
const maxPlausibleSessionDuration = 10 * time.Hour

// sessionRaw is one draft session as returned by the Worklog MCP server's
// get_sessions — field names mirror SessionView's own JSON tags exactly
// (cmd/mcp-worklog/sessions.go), redeclared here because that's a separate
// `main` package and can't be imported, the same reason
// WeeklySummaryGenerator.factsFor (weekly_summary.go) decodes its own
// local structs instead.
type sessionRaw struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Kind       string    `json:"kind"`
	Repo       string    `json:"repo,omitempty"`
	Title      string    `json:"title,omitempty"`
	EventCount int       `json:"event_count"`
	Project    string    `json:"project,omitempty"`
}

type getSessionsResult struct {
	Sessions []sessionRaw `json:"sessions"`
}

type getRepoSolidtimeProjectsResult struct {
	Repos []struct {
		Repo               string `json:"repo"`
		SolidtimeProjectID string `json:"solidtime_project_id"`
	} `json:"repos"`
}

// solidtimeTimeEntryRaw is one already-tracked Solidtime entry as returned
// by the Solidtime MCP server's list_time_entries (cmd/mcp-solidtime) —
// only the bounds matter here, the duplicate-overlap check doesn't care
// what the entry is for.
type solidtimeTimeEntryRaw struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type listTimeEntriesResult struct {
	Entries []solidtimeTimeEntryRaw `json:"entries"`
}

// AuditedSession is one draft session plus its audit outcome.
type AuditedSession struct {
	Start      time.Time   `json:"start"`
	End        time.Time   `json:"end"`
	Kind       string      `json:"kind"`
	Repo       string      `json:"repo,omitempty"`
	Title      string      `json:"title,omitempty"`
	EventCount int         `json:"event_count"`
	Project    string      `json:"project,omitempty"`
	Flags      []AuditFlag `json:"flags,omitempty"`
	// Remark is filled only by writeAuditRemarks (the chat command path) —
	// the raw REST endpoint never calls the LLM, so it stays empty there.
	Remark string `json:"remark,omitempty"`
}

// AuditResult is the full response of one audit run over a period. It
// never writes anything to Solidtime — read-only, by design, for every
// caller.
type AuditResult struct {
	From             time.Time        `json:"from"`
	To               time.Time        `json:"to"`
	Sessions         []AuditedSession `json:"sessions"`
	FlaggedCount     int              `json:"flagged_count"`
	SolidtimeChecked bool             `json:"solidtime_checked" jsonschema:"false means Solidtime wasn't configured or wasn't reachable — the duplicate and project-mapping checks ran with no Solidtime data to compare against"`
	Warnings         []string         `json:"warnings,omitempty"`
}

// overlaps reports whether [aStart,aEnd) and [bStart,bEnd) share any time.
func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// flagSession is the pure, deterministic core — same inputs always yield
// the same flags, independent of any LLM call. A calendar meeting
// overlapping a work session is deliberately not flagged here: the
// session pipeline (day 19/20) already gives meetings priority and never
// double-counts that time, so there's nothing for this audit to catch.
func flagSession(s sessionRaw, existingEntries []solidtimeTimeEntryRaw, solidtimeMapped map[string]bool) []AuditFlag {
	var flags []AuditFlag
	if !s.End.After(s.Start) || s.End.Sub(s.Start) > maxPlausibleSessionDuration {
		flags = append(flags, FlagSuspiciousDuration)
	}
	if s.Kind == "work" && !solidtimeMapped[s.Repo] {
		flags = append(flags, FlagUnmappedSolidtimeProject)
	}
	for _, e := range existingEntries {
		if overlaps(s.Start, s.End, e.Start, e.End) {
			flags = append(flags, FlagPossibleDuplicate)
			break
		}
	}
	return flags
}

// mcpCall is how fetchAndAudit makes an MCP tool call — abstracted so the
// REST endpoint (a plain conn.Call) and the /audit-timesheet chat command
// (a.callToolAsJSON, which also records a ToolCallRecord for the UI,
// agent_commands.go) can share this one implementation of the audit logic
// itself.
type mcpCall func(tool string, args map[string]any, out any) error

// fetchAndAudit runs the full audit for [from, to): get_sessions, the
// repo -> Solidtime project mapping, and — only when solidtimeConfigured —
// Solidtime's own already-tracked entries, then flags every session.
// Solidtime being unreachable (or not configured at all) degrades the
// audit, with an explicit warning, rather than failing it outright: the
// mapping/duration checks don't need Solidtime at all.
func fetchAndAudit(from, to time.Time, solidtimeConfigured bool, callWorklog, callSolidtime mcpCall) (AuditResult, error) {
	var sessionsResult getSessionsResult
	if err := callWorklog("get_sessions", map[string]any{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)}, &sessionsResult); err != nil {
		return AuditResult{}, fmt.Errorf("get_sessions: %w", err)
	}

	var mappingResult getRepoSolidtimeProjectsResult
	if err := callWorklog("get_repo_solidtime_projects", map[string]any{}, &mappingResult); err != nil {
		return AuditResult{}, fmt.Errorf("get_repo_solidtime_projects: %w", err)
	}
	mapped := make(map[string]bool, len(mappingResult.Repos))
	for _, r := range mappingResult.Repos {
		if r.SolidtimeProjectID != "" {
			mapped[r.Repo] = true
		}
	}

	var warnings []string
	var existingEntries []solidtimeTimeEntryRaw
	solidtimeChecked := false
	if solidtimeConfigured {
		var entriesResult listTimeEntriesResult
		if err := callSolidtime("list_time_entries", map[string]any{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)}, &entriesResult); err != nil {
			warnings = append(warnings, "Solidtime недоступен — проверка дублей не выполнена: "+err.Error())
		} else {
			existingEntries = entriesResult.Entries
			solidtimeChecked = true
		}
	} else {
		warnings = append(warnings, "Solidtime не настроен — проверка дублей недоступна")
	}

	result := AuditResult{From: from, To: to, SolidtimeChecked: solidtimeChecked, Warnings: warnings}
	result.Sessions = make([]AuditedSession, len(sessionsResult.Sessions))
	for i, s := range sessionsResult.Sessions {
		flags := flagSession(s, existingEntries, mapped)
		result.Sessions[i] = AuditedSession{
			Start: s.Start, End: s.End, Kind: s.Kind, Repo: s.Repo, Title: s.Title,
			EventCount: s.EventCount, Project: s.Project, Flags: flags,
		}
		if len(flags) > 0 {
			result.FlaggedCount++
		}
	}
	return result, nil
}

// mcpCallJSON is the REST endpoint's mcpCall — the direct-conn.Call
// equivalent of WeeklySummaryGenerator.call (weekly_summary.go), kept as
// its own small function rather than reusing that method so this day's
// addition can't regress that already-working generator.
func mcpCallJSON(ctx context.Context, conn *mcpclient.Conn, tool string, args map[string]any, out any) error {
	res, err := conn.Call(ctx, tool, args)
	if err != nil {
		return err
	}
	if res.IsError {
		return fmt.Errorf("%s", res.Text)
	}
	raw := res.Structured
	if len(raw) == 0 {
		raw = json.RawMessage(res.Text)
	}
	return json.Unmarshal(raw, out)
}

// auditRemarkRaw is one LLM-written remark, correlated back to its
// session by position among the FLAGGED sessions only (1-based) — never
// by repeating facts the model could get wrong, the same index-array
// pattern as rerankScore (rag_query.go).
type auditRemarkRaw struct {
	Index  int    `json:"index"`
	Remark string `json:"remark"`
}

const auditRemarkSystemPrompt = `Ты пишешь короткие замечания на русском языке к помеченным сессиям черновика таймшита — одно предложение на каждую. Используй ТОЛЬКО переданные факты и флаги, никогда не добавляй новых фактов и не меняй их смысл. Значения флагов:
- possible_duplicate: пересекается по времени с уже существующей записью в Solidtime — возможен повторный учёт.
- unmapped_solidtime_project: у репозитория нет явной привязки к проекту Solidtime — сопоставление не задано.
- suspicious_duration: нулевая/отрицательная или нетипично долгая длительность одной сессии.
Если у сессии несколько флагов, упомяни главный. Формулируй как "нужно проверить" / "стоит перепроверить", не как готовый вердикт "это ошибка". Ответь ТОЛЬКО JSON-массивом вида [{"index":1,"remark":"..."},...], без markdown и пояснений — по одному объекту на каждую переданную сессию, в том же порядке, что и во входных данных.`

// writeAuditRemarks fills Remark on every flagged session in result, with
// one LLM call covering all of them together. A malformed or failed
// response leaves every Remark empty rather than failing the whole
// audit — the flags themselves are already deterministic and useful on
// their own.
func writeAuditRemarks(ctx context.Context, client *LiteLLMClient, result *AuditResult) error {
	var flaggedIdx []int
	var prompt strings.Builder
	for i, s := range result.Sessions {
		if len(s.Flags) == 0 {
			continue
		}
		flaggedIdx = append(flaggedIdx, i)
		fmt.Fprintf(&prompt, "[%d] kind=%s repo=%s project=%s start=%s end=%s event_count=%d flags=%s\n",
			len(flaggedIdx), s.Kind, s.Repo, s.Project, s.Start.Format(time.RFC3339), s.End.Format(time.RFC3339), s.EventCount, joinFlags(s.Flags))
	}
	if len(flaggedIdx) == 0 {
		return nil
	}

	raw, err := client.chatComplete(ctx, []chatMessage{
		{Role: "system", Content: auditRemarkSystemPrompt},
		{Role: "user", Content: prompt.String()},
	}, 0.2, 2000, nil)
	if err != nil {
		return err
	}
	var parsed []auditRemarkRaw
	if err := json.Unmarshal([]byte(stripJSONArrayFences(raw)), &parsed); err != nil {
		return fmt.Errorf("decode remarks: %w (raw: %s)", err, truncateForLog(raw))
	}
	for _, p := range parsed {
		if p.Index < 1 || p.Index > len(flaggedIdx) {
			continue
		}
		result.Sessions[flaggedIdx[p.Index-1]].Remark = p.Remark
	}
	return nil
}

func joinFlags(flags []AuditFlag) string {
	s := make([]string, len(flags))
	for i, f := range flags {
		s[i] = string(f)
	}
	return strings.Join(s, ",")
}
