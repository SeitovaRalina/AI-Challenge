package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"aiwork/backend/internal/mcpclient"
)

func TestReportWeekRejectsInvalidISOWeek(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	for _, week := range []string{"2026-W00", "2026-W54", "2026-W99", "2027-W01", "bad"} {
		if _, _, _, err := reportWeek(now, week); err == nil {
			t.Errorf("accepted %q", week)
		}
	}
	week, from, to, err := reportWeek(now, "2026-W43")
	if err != nil || week != "2026-W43" || from.Day() != 19 || !to.Equal(now) {
		t.Fatalf("week=%q from=%v to=%v err=%v", week, from, to, err)
	}
}

func TestReportCollectReadsMultipleSourcesAndRendersStableDrafts(t *testing.T) {
	svc := &ReportService{worklog: mcpclient.NewConn(mcpclient.ServerConfig{}), dir: t.TempDir()}
	var called []string
	call := func(tool string, _ map[string]any, out any) error {
		called = append(called, tool)
		var raw string
		switch tool {
		case "get_sessions":
			raw = `{"sessions":[{"start":"2026-10-19T09:00:00+06:00","end":"2026-10-19T10:00:00+06:00","kind":"work","repo":"org/repo","project":"Project A","event_count":2}]}`
		case "get_repo_solidtime_projects":
			raw = `{"repos":[]}`
		case "get_analytics":
			raw = `{"kpi":{"total_hours":1,"meeting_hours":0,"active_days":1},"time_by_project":[{"project":"Project A","hours":1}]}`
		case "list_events":
			raw = `{"total":1,"events":[{"kind":"commit","title":"feat: work","repo":"org/repo","source":"github"}]}`
		default:
			t.Fatalf("unexpected tool %s", tool)
		}
		return json.Unmarshal([]byte(raw), out)
	}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	snapshot, err := svc.collectWithCalls(now, "2026-W43", call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"get_sessions", "get_repo_solidtime_projects", "get_analytics", "list_events"}; !reflect.DeepEqual(called, want) {
		t.Fatalf("tools=%v, want %v", called, want)
	}
	md1, md2 := renderWeeklyReport(snapshot), renderWeeklyReport(snapshot)
	if !bytes.Equal(md1, md2) || !bytes.Contains(md1, []byte("feat: work")) || !bytes.Contains(md1, []byte("Project A")) {
		t.Fatalf("report missing source facts or nondeterministic: %s", md1)
	}
	csvData, err := renderAuditedCSV(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(csvData))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][7] != "needs_review" || rows[1][6] != "worklog" || rows[1][9] != "false" {
		t.Fatalf("CSV failed to label source/draft/incomplete audit: %q", rows)
	}
}

func TestCSVNeverPromotesAuditedSessionToConfirmed(t *testing.T) {
	start := time.Date(2026, 10, 19, 9, 0, 0, 0, time.Local)
	snapshot := reportSnapshot{Audit: AuditResult{SolidtimeChecked: true, Sessions: []AuditedSession{
		{Start: start, End: start.Add(time.Hour), Kind: "work", Repo: "a/repo", Project: "A"},
		{Start: start.Add(time.Hour), End: start.Add(2 * time.Hour), Kind: "work", Repo: "b/repo", Project: "B", Flags: []AuditFlag{FlagPossibleDuplicate}},
		{Start: start, End: start.Add(time.Hour), Kind: "meeting", Title: "Review"},
	}}}
	data, err := renderAuditedCSV(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[1][7] != "audited_draft" || rows[2][7] != "needs_review" || rows[2][8] != "possible_duplicate" {
		t.Fatalf("incorrect draft statuses or meeting included: %q", rows)
	}
}
