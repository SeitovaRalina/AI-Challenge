package main

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aiwork/backend/internal/activity"
)

// get_analytics is day 19's read side: everything the Analytics screen's
// charts need, computed purely from stored sessions (get_sessions) joined
// with the repo→project mapping, plus a commit-type breakdown read directly
// from stored commit events. No LLM, no "productivity" score — counts and
// hours only, same principle as get_activity_digest in day 18.
//
// There is no development/review split here: for a workflow that's mostly
// commits, that split was almost always 100% development and told the user
// nothing. A conventional-commit type prefix (feat/fix/chore/...) in their
// own commit messages is a real signal instead — see commitTypeBreakdown.
//
// Day 20 adds meeting sessions into the same picture: resolveIntervals turns
// the period's sessions (work and meeting) into non-overlapping, kind-tagged
// blocks, giving meetings priority over work wherever they overlap, so every
// chart below (KPI, by-day, heatmap) reads real, deduplicated time — see
// resolveIntervals' own doc.

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
	TotalHours    float64 `json:"total_hours" jsonschema:"work + meeting hours, deduplicated — a minute inside a meeting is never also counted as work"`
	MeetingHours  float64 `json:"meeting_hours"`
	ActiveDays    int     `json:"active_days"`
	SessionsCount int     `json:"sessions_count" jsonschema:"work sessions only"`
	MeetingsCount int     `json:"meetings_count"`
	RepoCount     int     `json:"repo_count"`
	ProjectCount  int     `json:"project_count"`
}

type ProjectHours struct {
	Project string  `json:"project"`
	Hours   float64 `json:"hours"`
}

type DayHours struct {
	Date         string  `json:"date" jsonschema:"YYYY-MM-DD, local time"`
	Weekday      string  `json:"weekday"`
	WorkHours    float64 `json:"work_hours" jsonschema:"meeting overlap already subtracted"`
	MeetingHours float64 `json:"meeting_hours"`
	TotalHours   float64 `json:"total_hours" jsonschema:"work_hours + meeting_hours"`
}

type HeatmapCell struct {
	Weekday      int     `json:"weekday" jsonschema:"day of week, 0 is Sunday and 6 is Saturday (Go's time.Weekday)"`
	WeekdayLabel string  `json:"weekday_label"`
	Hour         int     `json:"hour" jsonschema:"0-23, local time"`
	AvgMinutes   float64 `json:"avg_minutes" jsonschema:"average minutes worked in this hour, on this weekday, per such day in the period — e.g. 12 for a Monday 09:00 cell means 12 minutes on average across every Monday in the period; always between 0 and 60, unlike a raw sum across weeks"`
}

type WeekTrend struct {
	WeekStart string  `json:"week_start" jsonschema:"YYYY-MM-DD, the week's Monday"`
	WeekEnd   string  `json:"week_end" jsonschema:"YYYY-MM-DD, the week's Sunday (or today, for the current week)"`
	Hours     float64 `json:"hours"`
}

// CommitTypeCount is how many commits in the period carried a given
// conventional-commit type prefix ("feat(scope): ..." -> "feat"; scope is
// dropped, only the type is kept). "other" covers commits with no
// recognized prefix.
type CommitTypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type AnalyticsOutput struct {
	From          time.Time         `json:"from"`
	To            time.Time         `json:"to"`
	Timezone      string            `json:"timezone"`
	KPI           KPI               `json:"kpi"`
	TimeByProject []ProjectHours    `json:"time_by_project" jsonschema:"most active project first; a repository with no explicit mapping is its own project (its bare name)"`
	ByDay         []DayHours        `json:"by_day"`
	CommitTypes   []CommitTypeCount `json:"commit_types" jsonschema:"commits in the period by conventional-commit type (feat/fix/chore/...), most frequent first; counts, not hours — an individual commit has no duration"`
	Heatmap       []HeatmapCell     `json:"heatmap" jsonschema:"all 168 weekday×hour cells, zero-filled; each cell is an average, not a sum — see HeatmapCell"`
	WeeklyTrend   []WeekTrend       `json:"weekly_trend" jsonschema:"the last 8 ISO weeks ending with the current one, regardless of the requested period"`
	Warnings      []string          `json:"warnings,omitempty"`
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
	workDayHours := map[string]float64{}
	meetingDayHours := map[string]float64{}
	activeDays := map[string]bool{}
	repos := map[string]bool{}
	projects := map[string]bool{}
	var heatmapMinutes [7][24]float64

	for _, row := range rows {
		sess := sessionViewFrom(row)
		if _, _, ok := clipInterval(sess.Start, sess.End, f.from, f.to); !ok {
			continue
		}
		if sess.Kind == SessionKindMeeting {
			out.KPI.MeetingsCount++
		} else {
			out.KPI.SessionsCount++
		}
	}

	for _, b := range resolveIntervals(rows, f.from, f.to) {
		hours := b.End.Sub(b.Start).Hours()
		out.KPI.TotalHours += hours
		if b.Kind == SessionKindMeeting {
			out.KPI.MeetingHours += hours
			addToDayMap(meetingDayHours, activeDays, b.Start, b.End)
			continue
		}
		projectHours[b.Project] += hours
		repos[b.Repo] = true
		projects[b.Project] = true
		addToDayMap(workDayHours, activeDays, b.Start, b.End)
		distributeHeatmapMinutes(&heatmapMinutes, b.Start, b.End)
	}
	out.KPI.ActiveDays = len(activeDays)
	out.KPI.RepoCount = len(repos)
	out.KPI.ProjectCount = len(projects)
	out.KPI.TotalHours = round2(out.KPI.TotalHours)
	out.KPI.MeetingHours = round2(out.KPI.MeetingHours)

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

	out.ByDay = buildByDay(workDayHours, meetingDayHours, f.from, f.to)

	commitTypes, err := s.commitTypeBreakdown(ctx, f.from, f.to)
	if err != nil {
		return nil, AnalyticsOutput{}, err
	}
	out.CommitTypes = commitTypes

	// A raw sum across every week in the period grows with the period's
	// length and can exceed 60 minutes for one hour — e.g. 4 Mondays each
	// contributing time to the same cell. Dividing by how many times that
	// weekday actually occurred in the period turns it into an average,
	// naturally bounded to [0, 60] and comparable across period lengths.
	weekdayOccurrences := countWeekdayOccurrences(f.from, f.to)
	out.Heatmap = make([]HeatmapCell, 0, 168)
	for wd := 0; wd < 7; wd++ {
		for h := 0; h < 24; h++ {
			var avg float64
			if weekdayOccurrences[wd] > 0 {
				avg = heatmapMinutes[wd][h] / float64(weekdayOccurrences[wd])
			}
			out.Heatmap = append(out.Heatmap, HeatmapCell{Weekday: wd, WeekdayLabel: weekdays[wd], Hour: h, AvgMinutes: round1(avg)})
		}
	}

	trend, err := s.weeklyTrend(ctx)
	if err != nil {
		return nil, AnalyticsOutput{}, err
	}
	out.WeeklyTrend = trend
	return nil, out, nil
}

// conventionalCommitType matches a Conventional Commits type prefix:
// "feat(scope): ..." or "feat!: ..." or plain "feat: ...". Group 1 is the
// type; the scope in group 2, if any, is deliberately discarded — the user
// asked for the type only.
var conventionalCommitType = regexp.MustCompile(`^([a-zA-Z]+)(\([^)]*\))?!?:\s`)

// knownCommitTypes is the standard Conventional Commits vocabulary (plus
// this project's own "chore"/"docs" usage); anything else — including a
// prefix-shaped but unrecognized word, or no prefix at all — is "other"
// rather than growing an unbounded, noisy set of one-off buckets.
var knownCommitTypes = map[string]bool{
	"feat": true, "fix": true, "docs": true, "style": true, "refactor": true,
	"perf": true, "test": true, "build": true, "ci": true, "chore": true, "revert": true,
}

func commitType(title string) string {
	m := conventionalCommitType.FindStringSubmatch(title)
	if m == nil {
		return "other"
	}
	t := strings.ToLower(m[1])
	if !knownCommitTypes[t] {
		return "other"
	}
	return t
}

// commitTypeBreakdown counts commit events in [from,to] by conventional-
// commit type, reading raw events directly (not sessions) — an individual
// commit's type is a property of the commit, unrelated to which session it
// fell into.
func (s *server) commitTypeBreakdown(ctx context.Context, from, to time.Time) ([]CommitTypeCount, error) {
	events, err := s.store.events(ctx, eventFilter{from: from, to: to, kinds: []activity.Kind{activity.KindCommit}}, 0)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, e := range events {
		counts[commitType(e.Title)]++
	}
	out := make([]CommitTypeCount, 0, len(counts))
	for t, n := range counts {
		out = append(out, CommitTypeCount{Type: t, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Type < out[j].Type
	})
	return out, nil
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

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

// countWeekdayOccurrences counts how many times each weekday's calendar date
// falls within [from,to] — e.g. how many Mondays a 30-day period contains —
// for turning a heatmap cell's summed minutes into a per-occurrence average.
func countWeekdayOccurrences(from, to time.Time) [7]int {
	var counts [7]int
	dayStart := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
	for d := dayStart; !d.After(to); d = d.AddDate(0, 0, 1) {
		counts[int(d.Weekday())]++
	}
	return counts
}

// addToDayMap splits [start,end] across the local calendar days it spans,
// accumulating hours into m and marking each touched day active.
func addToDayMap(m map[string]float64, activeDays map[string]bool, start, end time.Time) {
	cur := start
	for cur.Before(end) {
		dayStart := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, time.Local)
		dayEnd := dayStart.AddDate(0, 0, 1)
		segEnd := minTime(dayEnd, end)
		key := dayStart.Format("2006-01-02")
		m[key] += segEnd.Sub(cur).Hours()
		activeDays[key] = true
		cur = segEnd
	}
}

// block is a kind-tagged, non-overlapping span of time — the unit
// resolveIntervals produces and get_day_timeline exposes directly.
type block struct {
	Start, End time.Time
	Kind       string
	Repo       string
	Project    string
	Titles     []string
}

// resolveIntervals turns a period's session rows into kind-tagged,
// non-overlapping blocks clipped to [from,to]: meetings are merged where
// they themselves overlap (so two overlapping meetings are never counted as
// two meetings' worth of time), and any work session time that falls inside
// the resulting meeting union is subtracted — meetings take priority, so the
// same minute is never counted as both work and meeting.
func resolveIntervals(rows []storedSessionRow, from, to time.Time) []block {
	var meetingsRaw, work []block
	for _, row := range rows {
		sess := sessionViewFrom(row)
		start, end, ok := clipInterval(sess.Start, sess.End, from, to)
		if !ok {
			continue
		}
		b := block{Start: start, End: end, Kind: sess.Kind, Repo: sess.Repo, Project: sess.Project}
		if sess.Title != "" {
			b.Titles = []string{sess.Title}
		}
		if sess.Kind == SessionKindMeeting {
			meetingsRaw = append(meetingsRaw, b)
		} else {
			work = append(work, b)
		}
	}
	meetings := mergeMeetings(meetingsRaw)

	out := make([]block, 0, len(work)+len(meetings))
	for _, w := range work {
		for _, seg := range subtractBlocks(w, meetings) {
			seg.Kind, seg.Repo, seg.Project = SessionKindWork, w.Repo, w.Project
			out = append(out, seg)
		}
	}
	out = append(out, meetings...)
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// mergeMeetings sorts and merges overlapping (or touching) meeting blocks
// into a union, concatenating the titles of whatever merged into each one.
func mergeMeetings(raw []block) []block {
	if len(raw) == 0 {
		return nil
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Start.Before(raw[j].Start) })
	merged := []block{raw[0]}
	for _, b := range raw[1:] {
		last := &merged[len(merged)-1]
		if !b.Start.After(last.End) {
			if b.End.After(last.End) {
				last.End = b.End
			}
			last.Titles = append(last.Titles, b.Titles...)
			continue
		}
		merged = append(merged, b)
	}
	for i := range merged {
		merged[i].Kind = SessionKindMeeting
	}
	return merged
}

// subtractBlocks removes from w whatever time falls inside any of meetings
// (already sorted, non-overlapping), returning the remaining piece(s).
func subtractBlocks(w block, meetings []block) []block {
	segments := []block{w}
	for _, m := range meetings {
		var next []block
		for _, seg := range segments {
			if !m.Start.Before(seg.End) || !m.End.After(seg.Start) {
				next = append(next, seg) // no overlap
				continue
			}
			if m.Start.After(seg.Start) {
				next = append(next, block{Start: seg.Start, End: m.Start})
			}
			if m.End.Before(seg.End) {
				next = append(next, block{Start: m.End, End: seg.End})
			}
		}
		segments = next
	}
	return segments
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
func buildByDay(workHours, meetingHours map[string]float64, from, to time.Time) []DayHours {
	dayOf := func(key string) DayHours {
		w, m := workHours[key], meetingHours[key]
		return DayHours{WorkHours: round2(w), MeetingHours: round2(m), TotalHours: round2(w + m)}
	}
	days := map[string]bool{}
	for k := range workHours {
		days[k] = true
	}
	for k := range meetingHours {
		days[k] = true
	}

	out := []DayHours{}
	dayStart := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
	if to.Sub(dayStart) <= maxAnalyticsByDayDays*24*time.Hour {
		for d := dayStart; !d.After(to); d = d.AddDate(0, 0, 1) {
			key := d.Format("2006-01-02")
			day := dayOf(key)
			day.Date, day.Weekday = key, weekdays[d.Weekday()]
			out = append(out, day)
		}
		return out
	}
	for key := range days {
		day := dayOf(key)
		day.Date = key
		out = append(out, day)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	for i := range out {
		t, _ := time.ParseInLocation("2006-01-02", out[i].Date, time.Local)
		out[i].Weekday = weekdays[t.Weekday()]
	}
	return out
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
	for _, b := range resolveIntervals(rows, trendStart, now) {
		cur := b.Start
		for cur.Before(b.End) {
			weekStart := mondayOf(cur)
			weekEnd := weekStart.AddDate(0, 0, 7)
			segEnd := minTime(weekEnd, b.End)
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
