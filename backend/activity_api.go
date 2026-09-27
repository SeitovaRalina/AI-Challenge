package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"aiwork/backend/internal/mcpclient"
)

// REST for the «Активность» screen (day 18). The collector's own state comes
// from the Collector; the digest and the event feed are read from the
// Worklog MCP server — the UI sees exactly what the agent's tools see.

func activityStatusHandler(c *Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, c.Status(r.Context()))
	}
}

func activityCollectHandler(c *Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch err := c.Trigger(); {
		case errors.Is(err, ErrCollectorDisabled):
			writeError(w, http.StatusConflict, "Сбор выключен: "+c.disabledReason)
			return
		case errors.Is(err, ErrCollectorBusy):
			writeError(w, http.StatusConflict, "Сбор уже идёт")
			return
		}
		writeJSON(w, http.StatusAccepted, c.Status(r.Context()))
	}
}

// periodStart maps the screen's period switch to a start date.
func periodStart(period string, now time.Time) (string, bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch period {
	case "", "today":
		return today.Format("2006-01-02"), true
	case "7d":
		return today.AddDate(0, 0, -6).Format("2006-01-02"), true
	case "30d":
		return today.AddDate(0, 0, -29).Format("2006-01-02"), true
	}
	return "", false
}

func activityDigestHandler(worklog *mcpclient.Conn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, ok := periodStart(r.URL.Query().Get("period"), time.Now())
		if !ok {
			writeError(w, http.StatusBadRequest, "неизвестный период")
			return
		}
		callWorklog(w, r.Context(), worklog, "get_activity_digest", map[string]any{"from": from})
	}
}

func activityEventsHandler(worklog *mcpclient.Conn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		from, ok := periodStart(q.Get("period"), time.Now())
		if !ok {
			writeError(w, http.StatusBadRequest, "неизвестный период")
			return
		}
		args := map[string]any{"from": from, "limit": 200}
		if repo := strings.TrimSpace(q.Get("repo")); repo != "" {
			args["repos"] = []string{repo}
		}
		if kind := strings.TrimSpace(q.Get("kind")); kind != "" {
			args["kinds"] = []string{kind}
		}
		callWorklog(w, r.Context(), worklog, "list_events", args)
	}
}

// callWorklog passes a Worklog tool's structured result straight through.
func callWorklog(w http.ResponseWriter, ctx context.Context, worklog *mcpclient.Conn, tool string, args map[string]any) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := worklog.Call(ctx, tool, args)
	if err != nil {
		log.Printf("activity: worklog %s failed: %v", tool, err)
		writeError(w, http.StatusBadGateway, "Worklog MCP недоступен")
		return
	}
	if res.IsError {
		writeError(w, http.StatusBadGateway, "Worklog MCP вернул ошибку: "+res.Text)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.Structured)
}
