package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	for input, expected := range map[string]string{"": "2026-W43", "эту неделю": "2026-W43", "прошлую неделю": "2026-W42", "12.10.2026": "2026-W42", "2026-10-12": "2026-W42"} {
		got, _, _, err := reportWeek(now, input)
		if err != nil || got != expected {
			t.Errorf("input %q: week=%q err=%v, want %q", input, got, err, expected)
		}
	}
}

func TestWeeklyReportCommandAcceptsNaturalWeek(t *testing.T) {
	week, ok := parseWeeklyReportCommand("/weekly-report прошлую неделю")
	if !ok || week != "прошлую неделю" {
		t.Fatalf("week=%q ok=%t", week, ok)
	}
}

func TestReportRangeIncludesBothDatesAndRejectsInvalidOrder(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	from, to, endDate, err := reportRange(now, "2026-10-12", "2026-10-18")
	if err != nil || from.Day() != 12 || to.Day() != 19 || endDate.Day() != 18 {
		t.Fatalf("from=%v to=%v endDate=%v err=%v", from, to, endDate, err)
	}
	_, to, _, err = reportRange(now, "2026-10-19", "2026-10-19")
	if err != nil || !to.Equal(now) {
		t.Fatalf("today: to=%v err=%v", to, err)
	}
	for _, dates := range [][2]string{{"2026-10-20", "2026-10-20"}, {"2026-10-19", "2026-10-18"}, {"bad", "2026-10-19"}, {"2025-01-01", "2026-10-19"}} {
		if _, _, _, err := reportRange(now, dates[0], dates[1]); err == nil {
			t.Errorf("accepted range %v", dates)
		}
	}
}

func TestReportRangeFilesUseBothDates(t *testing.T) {
	svc := &ReportService{dir: t.TempDir()}
	snapshot := reportSnapshot{From: time.Date(2026, 10, 12, 0, 0, 0, 0, time.Local), To: time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local), EndDate: time.Date(2026, 10, 18, 0, 0, 0, 0, time.Local)}
	md, csv, err := svc.saveSnapshot(snapshot)
	if err != nil || md != "report-2026-10-12_to_2026-10-18.md" || csv != "timesheet-2026-10-12_to_2026-10-18.csv" {
		t.Fatalf("md=%q csv=%q err=%v", md, csv, err)
	}
	for _, name := range []string{md, csv} {
		req := httptest.NewRequest(http.MethodGet, "/api/reports/"+name, nil)
		req.SetPathValue("name", name)
		response := httptest.NewRecorder()
		reportDownloadHandler(svc)(response, req)
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("download %s: status=%d body=%q", name, response.Code, response.Body.String())
		}
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
