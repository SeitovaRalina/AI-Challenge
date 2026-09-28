package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"aiwork/backend/internal/mcpclient"
)

// REST for the «Аналитика» screen (day 19): everything comes straight from
// the Worklog MCP server's session/analytics tools — get_analytics for the
// charts, get_repo_projects/set_repo_project for the repo→project mapping
// UI. Same pass-through style as activity_api.go.

// analyticsPeriodStart maps the screen's period switch to a start date. 90d
// is analytics-specific (7d/30d/today are the «Активность» screen's); a
// meaningful trend needs more history than a daily activity feed does.
func analyticsPeriodStart(period string, now time.Time) (string, bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch period {
	case "", "7d":
		return today.AddDate(0, 0, -6).Format("2006-01-02"), true
	case "30d":
		return today.AddDate(0, 0, -29).Format("2006-01-02"), true
	case "90d":
		return today.AddDate(0, 0, -89).Format("2006-01-02"), true
	}
	return "", false
}

func getAnalyticsHandler(worklog *mcpclient.Conn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, ok := analyticsPeriodStart(r.URL.Query().Get("period"), time.Now())
		if !ok {
			writeError(w, http.StatusBadRequest, "неизвестный период")
			return
		}
		callWorklog(w, r.Context(), worklog, "get_analytics", map[string]any{"from": from})
	}
}

func listRepoProjectsHandler(worklog *mcpclient.Conn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callWorklog(w, r.Context(), worklog, "get_repo_projects", map[string]any{})
	}
}

type setRepoProjectRequest struct {
	Project string `json:"project"`
}

func setRepoProjectHandler(worklog *mcpclient.Conn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo := strings.TrimSpace(r.PathValue("repo"))
		if repo == "" {
			writeError(w, http.StatusBadRequest, "не указан репозиторий")
			return
		}
		var req setRepoProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		callWorklog(w, r.Context(), worklog, "set_repo_project", map[string]any{
			"repo": repo, "project": strings.TrimSpace(req.Project),
		})
	}
}
