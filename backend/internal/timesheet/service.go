package timesheet

import (
	"fmt"
	"time"
)

type Call func(tool string, args map[string]any, out any) error

type AuditedSession struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Kind       string    `json:"kind"`
	Repo       string    `json:"repo,omitempty"`
	Title      string    `json:"title,omitempty"`
	EventCount int       `json:"event_count"`
	Project    string    `json:"project,omitempty"`
	Flags      []Flag    `json:"flags,omitempty"`
	Remark     string    `json:"remark,omitempty"`
}

type AuditResult struct {
	From                  time.Time        `json:"from"`
	To                    time.Time        `json:"to"`
	Sessions              []AuditedSession `json:"sessions"`
	FlaggedCount          int              `json:"flagged_count"`
	SolidtimeChecked      bool             `json:"solidtime_checked"`
	SolidtimeTrackedHours *float64         `json:"solidtime_tracked_hours,omitempty"`
	Warnings              []string         `json:"warnings,omitempty"`
}

// FetchAndAudit reads the draft, explicit mapping and tracked time, then
// applies pure rules. Callers choose the MCP transport and optional tracing.
func FetchAndAudit(from, to time.Time, solidtimeConfigured bool, callWorklog, callSolidtime Call) (AuditResult, error) {
	var sessions struct {
		Sessions []Session `json:"sessions"`
	}
	if err := callWorklog("get_sessions", map[string]any{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)}, &sessions); err != nil {
		return AuditResult{}, fmt.Errorf("get_sessions: %w", err)
	}
	var mapping struct {
		Repos []struct {
			Repo               string `json:"repo"`
			SolidtimeProjectID string `json:"solidtime_project_id"`
		} `json:"repos"`
	}
	if err := callWorklog("get_repo_solidtime_projects", map[string]any{}, &mapping); err != nil {
		return AuditResult{}, fmt.Errorf("get_repo_solidtime_projects: %w", err)
	}
	mapped := make(map[string]bool, len(mapping.Repos))
	for _, row := range mapping.Repos {
		if row.SolidtimeProjectID != "" {
			mapped[row.Repo] = true
		}
	}

	result := AuditResult{From: from, To: to, Sessions: make([]AuditedSession, len(sessions.Sessions))}
	var existing []ExistingEntry
	if solidtimeConfigured {
		var entries struct {
			Entries []ExistingEntry `json:"entries"`
		}
		if err := callSolidtime("list_time_entries", map[string]any{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)}, &entries); err != nil {
			result.Warnings = append(result.Warnings, "Solidtime недоступен — проверка дублей не выполнена: "+err.Error())
		} else {
			existing = entries.Entries
			result.SolidtimeChecked = true
			var tracked time.Duration
			for _, entry := range existing {
				start, end := entry.Start, entry.End
				if start.Before(from) {
					start = from
				}
				if end.After(to) {
					end = to
				}
				if end.After(start) {
					tracked += end.Sub(start)
				}
			}
			hours := float64(tracked) / float64(time.Hour)
			result.SolidtimeTrackedHours = &hours
		}
	} else {
		result.Warnings = append(result.Warnings, "Solidtime не настроен — проверка дублей недоступна")
	}
	for i, s := range sessions.Sessions {
		flags := FlagSession(s, existing, mapped)
		result.Sessions[i] = AuditedSession{Start: s.Start, End: s.End, Kind: s.Kind, Repo: s.Repo, Title: s.Title, EventCount: s.EventCount, Project: s.Project, Flags: flags}
		if len(flags) > 0 {
			result.FlaggedCount++
		}
	}
	return result, nil
}
