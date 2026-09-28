package main

import (
	"context"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aiwork/backend/internal/activity"
)

// get_analytics is day 19's read side: everything the Analytics screen's
// charts need, computed purely from stored sessions (get_sessions) joined
// with the repo→project mapping. No LLM, no "productivity" score — counts
// and hours only, same principle as get_activity_digest in day 18.

// maxAnalyticsByDayDays bounds how long a period may be for by_day to list
// every day (empty ones included); the screen only ever asks for up to 90
// days, comfortably under this.
const maxAnalyticsByDayDays = 120

// weeklyTrendWeeks is how many ISO (Monday-start) weeks the weekly trend
// covers, ending with the current, possibly partial, week — independent of
// the requested period, since a trend over the period alone (e.g. 7 days)
// wouldn't show a trend at all.
const weeklyTrendWeeks = 8

type AnalyticsInput struct {
	From string `json:"from" jsonschema:"start of the period: RFC3339 timestamp, or a YYYY-MM-DD date (local time)"`
	To   string `json:"to,omitempty" jsonschema:"end of the period; defaults to now"`
}

type KPI struct {
	TotalHours       float64 `json:"total_hours"`
	DevelopmentHours float64 `json:"development_hours"`
	ReviewHours      float64 `json:"review_hours"`
	ActiveDays       int     `json:"active_days"`
	SessionsCount    int     `json:"sessions_count"`
	RepoCount        int     `json:"repo_count"`
	ProjectCount     int     `json:"project_count"`
}

type ProjectHours struct {
	Project string  `json:"project"`
	Hours   float64 `json:"hours"`
}

type DayHours struct {
	Date             string  `json:"date" jsonschema:"YYYY-MM-DD, local time"`
	Weekday          string  `json:"weekday"`
	DevelopmentHours float64 `json:"development_hours"`
	ReviewHours      float64 `json:"review_hours"`
	OtherHours       float64 `json:"other_hours"`
	TotalHours       float64 `json:"total_hours"`
}

type CategoryHours struct {
	Category Category `json:"category"`
	Hours    float64  `json:"hours"`
}

type HeatmapCell struct {
	Weekday      int    `json:"weekday" jsonschema:"day of week, 0 is Sunday and 6 is Saturday (Go's time.Weekday)"`
	WeekdayLabel string `json:"weekday_label"`
	Hour         int    `json:"hour" jsonschema:"0-23, local time"`
	Minutes      int    `json:"minutes"`
}

type WeekTrend struct {
	WeekStart string  `json:"week_start" jsonschema:"YYYY-MM-DD, the week's Monday"`
	WeekEnd   string  `json:"week_end" jsonschema:"YYYY-MM-DD, the week's Sunday (or today, for the current week)"`
	Hours     float64 `json:"hours"`
}

type AnalyticsOutput struct {
	From          time.Time       `json:"from"`
	To            time.Time       `json:"to"`
	Timezone      string          `json:"timezone"`
	KPI           KPI             `json:"kpi"`
	TimeByProject []ProjectHours  `json:"time_by_project" jsonschema:"most active project first; unmapped repositories under \"Без проекта\""`
	ByDay         []DayHours      `json:"by_day"`
	Composition   []CategoryHours `json:"composition" jsonschema:"development/review/other totals over the whole period"`
	Heatmap       []HeatmapCell   `json:"heatmap" jsonschema:"all 168 weekday×hour cells, zero-filled"`
	WeeklyTrend   []WeekTrend     `json:"weekly_trend" jsonschema:"the last 8 ISO weeks ending with the current one, regardless of the requested period"`
	Warnings      []string        `json:"warnings,omitempty"`
}

func (s *server) getAnalytics(ctx context.Context, _ *mcp.CallToolRequest, in AnalyticsInput) (*mcp.CallToolResult, AnalyticsOutput, error) {
	f, warnings, err := s.resolveSessionFilter(ctx, in.From, in.To, nil, "", maxDigestWindow)
	if err != nil {
		return nil, AnalyticsOutput{}, err
	}
	rows, err := s.store.sessions(ctx, f)
	if err != nil {
		return nil, AnalyticsOutput{}, err
	}

	out := AnalyticsOutput{From: f.from.In(time.Local), To: f.to.In(time.Local), Timezone: activity.LocalZoneName(), Warnings: warnings}
	projectHours := map[string]float64{}
	categoryHours := map[Category]float64{}
	dayHours := map[string]*DayHours{}
	activeDays := map[string]bool{}
	repos := map[string]bool{}
	projects := map[string]bool{}
	var heatmapMinutes [7][24]float64

	for _, row := range rows {
		sess := sessionViewFrom(row)
		start, end, ok := clipInterval(sess.Start, sess.End, f.from, f.to)
		if !ok {
			continue
		}
		hours := end.Sub(start).Hours()

		out.KPI.SessionsCount++
		out.KPI.TotalHours += hours
		categoryHours[sess.Category] += hours
		if sess.Category == CategoryDevelopment {
			out.KPI.DevelopmentHours += hours
		} else if sess.Category == CategoryReview {
			out.KPI.ReviewHours += hours
		}
		projectHours[sess.Project] += hours
		repos[sess.Repo] = true
		projects[sess.Project] = true

		distributeDayHours(dayHours, activeDays, sess.Category, start, end)
		distributeHeatmapMinutes(&heatmapMinutes, start, end)
	}
	out.KPI.ActiveDays = len(activeDays)
	out.KPI.RepoCount = len(repos)
	out.KPI.ProjectCount = len(projects)
	out.KPI.TotalHours = round2(out.KPI.TotalHours)
	out.KPI.DevelopmentHours = round2(out.KPI.DevelopmentHours)
	out.KPI.ReviewHours = round2(out.KPI.ReviewHours)

	out.TimeByProject = make([]ProjectHours, 0, len(projectHours))
	for project, hours := range projectHours {
		out.TimeByProject = append(out.TimeByProject, ProjectHours{Project: project, Hours: round2(hours)})
	}
	sort.Slice(out.TimeByProject, func(i, j int) bool {
		if out.TimeByProject[i].Hours != out.TimeByProject[j].Hours {
			return out.TimeByProject[i].Hours > out.TimeByProject[j].Hours
		}
		return out.TimeByProject[i].Project < out.TimeByProject[j].Project
	})

	out.Composition = []CategoryHours{
		{Category: CategoryDevelopment, Hours: round2(categoryHours[CategoryDevelopment])},
		{Category: CategoryReview, Hours: round2(categoryHours[CategoryReview])},
		{Category: CategoryOther, Hours: round2(categoryHours[CategoryOther])},
	}

	out.ByDay = buildByDay(dayHours, f.from, f.to)

	out.Heatmap = make([]HeatmapCell, 0, 168)
	for wd := 0; wd < 7; wd++ {
		for h := 0; h < 24; h++ {
			out.Heatmap = append(out.Heatmap, HeatmapCell{Weekday: wd, WeekdayLabel: weekdays[wd], Hour: h, Minutes: int(heatmapMinutes[wd][h] + 0.5)})
		}
	}

	trend, err := s.weeklyTrend(ctx)
	if err != nil {
		return nil, AnalyticsOutput{}, err
	}
	out.WeeklyTrend = trend
	return nil, out, nil
}

// clipInterval intersects [start,end] with [from,to]; ok is false when they
// don't overlap (shouldn't happen given the caller already filtered by
// overlap, but a session store is shared state — defend anyway).
func clipInterval(start, end, from, to time.Time) (clippedStart, clippedEnd time.Time, ok bool) {
	clippedStart, clippedEnd = maxTime(start, from), minTime(end, to)
	return clippedStart, clippedEnd, clippedEnd.After(clippedStart)
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

// distributeDayHours splits [start,end] across the local calendar days it
// spans, crediting each day's slice to sess's category.
func distributeDayHours(days map[string]*DayHours, activeDays map[string]bool, category Category, start, end time.Time) {
	cur := start
	for cur.Before(end) {
		dayStart := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, time.Local)
		dayEnd := dayStart.AddDate(0, 0, 1)
		segEnd := minTime(dayEnd, end)
		hours := segEnd.Sub(cur).Hours()

		key := dayStart.Format("2006-01-02")
		d := days[key]
		if d == nil {
			d = &DayHours{Date: key, Weekday: weekdays[dayStart.Weekday()]}
			days[key] = d
		}
		switch category {
		case CategoryDevelopment:
			d.DevelopmentHours += hours
		case CategoryReview:
			d.ReviewHours += hours
		default:
			d.OtherHours += hours
		}
		d.TotalHours += hours
		activeDays[key] = true
		cur = segEnd
	}
}

// distributeHeatmapMinutes splits [start,end] across the local weekday×hour
// cells it spans.
func distributeHeatmapMinutes(grid *[7][24]float64, start, end time.Time) {
	cur := start
	for cur.Before(end) {
		hourStart := time.Date(cur.Year(), cur.Month(), cur.Day(), cur.Hour(), 0, 0, 0, time.Local)
		hourEnd := hourStart.Add(time.Hour)
		segEnd := minTime(hourEnd, end)
		grid[int(cur.Weekday())][cur.Hour()] += segEnd.Sub(cur).Minutes()
		cur = segEnd
	}
}

// buildByDay renders the accumulated per-day totals as a sorted list — every
// day in [from,to] when the period is short enough to make that useful,
// otherwise only days with any activity.
func buildByDay(days map[string]*DayHours, from, to time.Time) []DayHours {
	out := []DayHours{}
	dayStart := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
	if to.Sub(dayStart) <= maxAnalyticsByDayDays*24*time.Hour {
		for d := dayStart; !d.After(to); d = d.AddDate(0, 0, 1) {
			key := d.Format("2006-01-02")
			if day := days[key]; day != nil {
				out = append(out, roundDay(*day))
			} else {
				out = append(out, DayHours{Date: key, Weekday: weekdays[d.Weekday()]})
			}
		}
		return out
	}
	for _, d := range days {
		out = append(out, roundDay(*d))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func roundDay(d DayHours) DayHours {
	d.DevelopmentHours, d.ReviewHours, d.OtherHours, d.TotalHours = round2(d.DevelopmentHours), round2(d.ReviewHours), round2(d.OtherHours), round2(d.TotalHours)
	return d
}

// weeklyTrend queries sessions independently of get_analytics' own period —
// always the trailing weeklyTrendWeeks Monday-start weeks ending with the
// current one.
func (s *server) weeklyTrend(ctx context.Context) ([]WeekTrend, error) {
	now := time.Now()
	thisMonday := mondayOf(now)
	trendStart := thisMonday.AddDate(0, 0, -7*(weeklyTrendWeeks-1))

	rows, err := s.store.sessions(ctx, sessionFilter{from: trendStart, to: now})
	if err != nil {
		return nil, err
	}
	byWeek := map[string]float64{}
	for _, row := range rows {
		sess := sessionViewFrom(row)
		start, end, ok := clipInterval(sess.Start, sess.End, trendStart, now)
		if !ok {
			continue
		}
		cur := start
		for cur.Before(end) {
			weekStart := mondayOf(cur)
			weekEnd := weekStart.AddDate(0, 0, 7)
			segEnd := minTime(weekEnd, end)
			byWeek[weekStart.Format("2006-01-02")] += segEnd.Sub(cur).Hours()
			cur = segEnd
		}
	}

	trend := make([]WeekTrend, 0, weeklyTrendWeeks)
	for w := trendStart; !w.After(thisMonday); w = w.AddDate(0, 0, 7) {
		sunday := w.AddDate(0, 0, 6)
		end := sunday
		if end.After(now) {
			end = now
		}
		trend = append(trend, WeekTrend{
			WeekStart: w.Format("2006-01-02"),
			WeekEnd:   end.Format("2006-01-02"),
			Hours:     round2(byWeek[w.Format("2006-01-02")]),
		})
	}
	return trend, nil
}

// mondayOf returns local midnight of the Monday of t's week.
func mondayOf(t time.Time) time.Time {
	daysSinceMonday := (int(t.Weekday()) + 6) % 7 // Mon=0 ... Sun=6
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	return d.AddDate(0, 0, -daysSinceMonday)
}
