package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"aiwork/backend/internal/timesheet"
)

// Day 32: deterministic flagging first (flagSession below) — the model
// never decides whether a session needs a human look, only how to phrase
// that in Russian, for the sessions already singled out (writeAuditRemarks).
// Same discipline as /work-summary (agent_commands.go, day 31) and
// rerankLLM's index-correlated JSON array (rag_query.go, day 23).

// AuditFlag is one objective, programmatically-detected reason a draft
// session needs a human look before being trusted as a timesheet entry.
type AuditFlag = timesheet.Flag

const (
	// FlagPossibleDuplicate: the session's time range overlaps a time
	// entry already tracked in Solidtime — syncing it as-is risks counting
	// the same work twice.
	FlagPossibleDuplicate AuditFlag = timesheet.PossibleDuplicate
	// FlagUnmappedSolidtimeProject: the session's repository has no
	// explicit Solidtime project mapping yet (set_repo_solidtime_project,
	// cmd/mcp-worklog/solidtime_mapping.go) — never guessed by matching
	// names, since a repo and its Solidtime project name routinely differ.
	FlagUnmappedSolidtimeProject AuditFlag = timesheet.UnmappedSolidtimeProject
	// FlagSuspiciousDuration: zero/negative, or implausibly long for one
	// contiguous block.
	FlagSuspiciousDuration AuditFlag = timesheet.SuspiciousDuration
)

// maxPlausibleSessionDuration: a single work session longer than this is
// almost certainly several real blocks the lead-in/gap heuristic
// (cmd/mcp-worklog/sessions.go) merged too eagerly, not one real sitting.
const maxPlausibleSessionDuration = timesheet.MaxPlausibleDuration

// sessionRaw is one draft session as returned by the Worklog MCP server's
// get_sessions — field names mirror SessionView's JSON tags. The shared
// audit component owns the shape; this alias keeps existing callers stable.
type sessionRaw = timesheet.Session

// solidtimeTimeEntryRaw is one already-tracked Solidtime entry as returned
// by the Solidtime MCP server's list_time_entries (cmd/mcp-solidtime) —
// only the bounds matter here, the duplicate-overlap check doesn't care
// what the entry is for.
type solidtimeTimeEntryRaw = timesheet.ExistingEntry

// The engine lives in internal/timesheet; these aliases preserve the existing
// REST and chat response types while both callers use one implementation.
type AuditedSession = timesheet.AuditedSession
type AuditResult = timesheet.AuditResult
type mcpCall = timesheet.Call

func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func flagSession(s sessionRaw, existingEntries []solidtimeTimeEntryRaw, solidtimeMapped map[string]bool) []AuditFlag {
	return timesheet.FlagSession(s, existingEntries, solidtimeMapped)
}

func fetchAndAudit(from, to time.Time, solidtimeConfigured bool, callWorklog, callSolidtime mcpCall) (AuditResult, error) {
	return timesheet.FetchAndAudit(from, to, solidtimeConfigured, callWorklog, callSolidtime)
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
