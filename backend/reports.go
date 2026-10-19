package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aiwork/backend/internal/mcpclient"
)

// ReportService keeps source collection separate from rendering and HTTP.
// Both artifacts use the same audited snapshot for a selected ISO week.
type ReportService struct {
	worklog   *mcpclient.Conn
	solidtime *mcpclient.Conn
	dir       string
}

type reportEvent struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Repo   string `json:"repo"`
	Source string `json:"source"`
}

type reportAnalytics struct {
	KPI struct {
		TotalHours   float64 `json:"total_hours"`
		MeetingHours float64 `json:"meeting_hours"`
		ActiveDays   int     `json:"active_days"`
	} `json:"kpi"`
	TimeByProject []struct {
		Project string  `json:"project"`
		Hours   float64 `json:"hours"`
	} `json:"time_by_project"`
	Warnings []string `json:"warnings"`
}

type reportSnapshot struct {
	Week       string
	From       time.Time
	To         time.Time
	Analytics  reportAnalytics
	Events     []reportEvent
	EventTotal int
	Audit      AuditResult
}

func reportWeek(now time.Time, requested string) (string, time.Time, time.Time, error) {
	if requested == "" {
		y, w := now.ISOWeek()
		requested = fmt.Sprintf("%04d-W%02d", y, w)
	}
	if len(requested) != 8 || requested[4] != '-' || requested[5] != 'W' {
		return "", time.Time{}, time.Time{}, fmt.Errorf("week must be YYYY-WW")
	}
	year, yerr := strconv.Atoi(requested[:4])
	week, werr := strconv.Atoi(requested[6:])
	if yerr != nil || werr != nil || week < 1 || week > 53 || year < 2000 || year > 2100 {
		return "", time.Time{}, time.Time{}, fmt.Errorf("invalid ISO week")
	}
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.Local)
	from, _ := weekBounds(jan4)
	from = from.AddDate(0, 0, 7*(week-1))
	if y, w := from.ISOWeek(); y != year || w != week {
		return "", time.Time{}, time.Time{}, fmt.Errorf("invalid ISO week")
	}
	to := from.AddDate(0, 0, 7)
	if from.After(now) {
		return "", time.Time{}, time.Time{}, fmt.Errorf("future week is unavailable")
	}
	if to.After(now) {
		to = now
	}
	return requested, from, to, nil
}

func (s *ReportService) collect(ctx context.Context, now time.Time, week string) (reportSnapshot, error) {
	callWorklog, callSolidtime := auditMCPCalls(ctx, s.worklog, s.solidtime)
	return s.collectWithCalls(now, week, callWorklog, callSolidtime)
}

func (s *ReportService) collectWithCalls(now time.Time, week string, callWorklog, callSolidtime mcpCall) (reportSnapshot, error) {
	week, from, to, err := reportWeek(now, week)
	if err != nil {
		return reportSnapshot{}, err
	}
	if s.worklog == nil {
		return reportSnapshot{}, fmt.Errorf("Worklog MCP unavailable")
	}
	audit, err := fetchAndAudit(from, to, s.solidtime != nil, callWorklog, callSolidtime)
	if err != nil {
		return reportSnapshot{}, err
	}
	args := map[string]any{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)}
	var analytics reportAnalytics
	if err := callWorklog("get_analytics", args, &analytics); err != nil {
		return reportSnapshot{}, fmt.Errorf("get_analytics: %w", err)
	}
	var events struct {
		Events []reportEvent `json:"events"`
		Total  int           `json:"total"`
	}
	if err := callWorklog("list_events", map[string]any{"from": args["from"], "to": args["to"], "limit": 500}, &events); err != nil {
		return reportSnapshot{}, fmt.Errorf("list_events: %w", err)
	}
	return reportSnapshot{Week: week, From: from, To: to, Analytics: analytics, Events: events.Events, EventTotal: events.Total, Audit: audit}, nil
}

func cleanReportText(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func renderWeeklyReport(s reportSnapshot) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Рабочий отчёт: %s\n\nПериод: %s — %s. Данные из Worklog; часы сессий — черновик до подтверждения.\n\n", s.Week, s.From.Format("2006-01-02"), s.To.Format("2006-01-02"))
	fmt.Fprintf(&b, "## Время\n\n- Всего: %.2f ч\n- Встречи: %.2f ч\n- Активных дней: %d\n\n", s.Analytics.KPI.TotalHours, s.Analytics.KPI.MeetingHours, s.Analytics.KPI.ActiveDays)
	b.WriteString("## Проекты\n\n")
	projects := append([]struct {
		Project string  `json:"project"`
		Hours   float64 `json:"hours"`
	}(nil), s.Analytics.TimeByProject...)
	sort.Slice(projects, func(i, j int) bool { return projects[i].Project < projects[j].Project })
	if len(projects) == 0 {
		b.WriteString("Нет учтённых проектов.\n")
	}
	for _, p := range projects {
		fmt.Fprintf(&b, "- %s: %.2f ч\n", cleanReportText(p.Project), p.Hours)
	}
	fmt.Fprintf(&b, "\n## Сделано\n\nСобытий: %d.\n", s.EventTotal)
	items := make([]string, 0, len(s.Events))
	for _, e := range s.Events {
		if e.Kind == "meeting" || e.Title == "" {
			continue
		}
		items = append(items, fmt.Sprintf("- %s [%s; %s]", cleanReportText(e.Title), cleanReportText(e.Repo), cleanReportText(e.Source)))
	}
	sort.Strings(items)
	if len(items) == 0 {
		b.WriteString("Нет событий с описанием.\n")
	}
	for _, item := range items {
		b.WriteString(item + "\n")
	}
	if s.EventTotal > len(s.Events) {
		fmt.Fprintf(&b, "\nПоказаны последние %d из %d событий.\n", len(s.Events), s.EventTotal)
	}
	fmt.Fprintf(&b, "\n## Сверка с таймшитом\n\n- Черновых сессий: %d\n- Требуют проверки: %d\n- Проверка Solidtime: %t\n", len(s.Audit.Sessions), s.Audit.FlaggedCount, s.Audit.SolidtimeChecked)
	if s.Audit.SolidtimeTrackedHours != nil {
		fmt.Fprintf(&b, "- Уже записано в Solidtime: %.2f ч\n", *s.Audit.SolidtimeTrackedHours)
		fmt.Fprintf(&b, "- Разница собранного и записанного времени: %+.2f ч (требует проверки, не готовая запись)\n", s.Analytics.KPI.TotalHours-*s.Audit.SolidtimeTrackedHours)
	}
	for _, warning := range s.Audit.Warnings {
		fmt.Fprintf(&b, "- Ограничение: %s\n", cleanReportText(warning))
	}
	for _, warning := range s.Analytics.Warnings {
		fmt.Fprintf(&b, "- Ограничение аналитики: %s\n", cleanReportText(warning))
	}
	if s.Audit.FlaggedCount > 0 {
		b.WriteString("\n## Открытые вопросы\n\n")
		for _, row := range s.Audit.Sessions {
			if len(row.Flags) == 0 {
				continue
			}
			fmt.Fprintf(&b, "- %s %s: %s (%s)\n", row.Start.Format("2006-01-02 15:04"), cleanReportText(row.Repo), cleanReportText(row.Project), joinFlags(row.Flags))
		}
	}
	return []byte(b.String())
}

func renderAuditedCSV(s reportSnapshot) ([]byte, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write([]string{"start", "end", "hours", "kind", "repo", "project", "source", "status", "audit_flags", "solidtime_checked"}); err != nil {
		return nil, err
	}
	rows := append([]AuditedSession(nil), s.Audit.Sessions...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Start.Equal(rows[j].Start) {
			return rows[i].Repo < rows[j].Repo
		}
		return rows[i].Start.Before(rows[j].Start)
	})
	for _, row := range rows {
		if row.Kind != "work" {
			continue
		}
		status := "audited_draft"
		if !s.Audit.SolidtimeChecked {
			status = "audit_incomplete"
		}
		if len(row.Flags) > 0 {
			status = "needs_review"
		}
		if err := w.Write([]string{row.Start.Format(time.RFC3339), row.End.Format(time.RFC3339), fmt.Sprintf("%.2f", row.End.Sub(row.Start).Hours()), row.Kind, row.Repo, row.Project, "worklog", status, joinFlags(row.Flags), strconv.FormatBool(s.Audit.SolidtimeChecked)}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func (s *ReportService) generate(ctx context.Context, now time.Time, week string) (string, string, error) {
	snapshot, err := s.collect(ctx, now, week)
	if err != nil {
		return "", "", err
	}
	return s.saveSnapshot(snapshot)
}

func (s *ReportService) saveSnapshot(snapshot reportSnapshot) (string, string, error) {
	markdown := renderWeeklyReport(snapshot)
	csvData, err := renderAuditedCSV(snapshot)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", "", err
	}
	mdName := "weekly-" + snapshot.Week + ".md"
	csvName := "timesheet-" + snapshot.Week + ".csv"
	if err := writeReportFile(filepath.Join(s.dir, mdName), markdown); err != nil {
		return "", "", err
	}
	if err := writeReportFile(filepath.Join(s.dir, csvName), csvData); err != nil {
		return "", "", err
	}
	return mdName, csvName, nil
}

func writeReportFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".report-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func reportGenerateHandler(s *ReportService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		week, _, _, err := reportWeek(now, r.URL.Query().Get("week"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "Неверная неделя: "+err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		md, csv, err := s.generate(ctx, now, week)
		if err != nil {
			writeError(w, http.StatusBadGateway, "Не удалось создать отчёт: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"markdown": md, "csv": csv})
	}
}

func reportDownloadHandler(s *ReportService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name != filepath.Base(name) || !(strings.HasPrefix(name, "weekly-") && strings.HasSuffix(name, ".md") || strings.HasPrefix(name, "timesheet-") && strings.HasSuffix(name, ".csv")) {
			writeError(w, http.StatusBadRequest, "Неверное имя файла")
			return
		}
		if _, _, _, err := reportWeek(time.Now(), strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(name, "weekly-"), "timesheet-"), filepath.Ext(name))); err != nil {
			writeError(w, http.StatusBadRequest, "Неверная неделя")
			return
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
		if strings.HasSuffix(name, ".csv") {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		}
		http.ServeFile(w, r, filepath.Join(s.dir, name))
	}
}
