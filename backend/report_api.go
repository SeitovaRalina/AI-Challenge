package main

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func reportGenerateHandler(s *ReportService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		start, end := r.URL.Query().Get("from"), r.URL.Query().Get("to")
		week := r.URL.Query().Get("week")
		if start != "" || end != "" {
			if _, _, _, err := reportRange(now, start, end); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		} else {
			var err error
			week, _, _, err = reportWeek(now, week)
			if err != nil {
				writeError(w, http.StatusBadRequest, "Неверная неделя: "+err.Error())
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		var md, csv string
		var err error
		if start != "" || end != "" {
			md, csv, err = s.generateRange(ctx, now, start, end)
		} else {
			md, csv, err = s.generate(ctx, now, week)
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, "Не удалось создать отчёт: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"markdown": md, "csv": csv})
	}
}

var reportFilenamePattern = regexp.MustCompile(`^(weekly-\d{4}-W\d{2}\.md|report-\d{4}-\d{2}-\d{2}_to_\d{4}-\d{2}-\d{2}\.md|timesheet-(\d{4}-W\d{2}|\d{4}-\d{2}-\d{2}_to_\d{4}-\d{2}-\d{2})\.csv)$`)

func reportDownloadHandler(s *ReportService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name != filepath.Base(name) || !reportFilenamePattern.MatchString(name) {
			writeError(w, http.StatusBadRequest, "Неверное имя файла")
			return
		}
		stem := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(name, "weekly-"), "report-"), "timesheet-"), filepath.Ext(name))
		if strings.Contains(stem, "_to_") {
			parts := strings.SplitN(stem, "_to_", 2)
			if _, _, _, err := reportRange(time.Now(), parts[0], parts[1]); err != nil {
				writeError(w, http.StatusBadRequest, "Неверный период")
				return
			}
		} else if _, _, _, err := reportWeek(time.Now(), stem); err != nil {
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
