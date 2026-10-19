package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func parseWeeklyReportCommand(message string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(message))
	if len(parts) == 0 || strings.ToLower(parts[0]) != "/weekly-report" {
		return "", false
	}
	if len(parts) > 1 {
		return strings.Join(parts[1:], " "), true
	}
	return "", true
}

func (a *Agent) answerWeeklyReport(ctx context.Context, chatID, week string) (string, []ToolCallRecord, error) {
	if a.reports == nil || a.reports.worklog == nil {
		return "Отчёты сейчас недоступны.", nil, nil
	}
	var calls []ToolCallRecord
	callWorklog := func(tool string, args map[string]any, out any) error {
		record, err := a.callToolAsJSON(ctx, chatID, a.reports.worklog, tool, args, out)
		calls = append(calls, record)
		return err
	}
	var callSolidtime mcpCall
	if a.reports.solidtime != nil {
		callSolidtime = func(tool string, args map[string]any, out any) error {
			record, err := a.callToolAsJSON(ctx, chatID, a.reports.solidtime, tool, args, out)
			calls = append(calls, record)
			return err
		}
	}
	snapshot, err := a.reports.collectWithCalls(time.Now(), week, callWorklog, callSolidtime)
	if err != nil {
		return "", calls, err
	}
	md, csv, err := a.reports.saveSnapshot(snapshot)
	if err != nil {
		return "", calls, err
	}
	weekEnd := snapshot.From.AddDate(0, 0, 6)
	return fmt.Sprintf("Готово: неделя %s–%s. Отчёт: [%s](/api/reports/%s). CSV с черновыми записями и статусом аудита: [%s](/api/reports/%s). Файлы сохранены в каталоге reports этого профиля.", snapshot.From.Format("02.01.2006"), weekEnd.Format("02.01.2006"), md, md, csv, csv), calls, nil
}
