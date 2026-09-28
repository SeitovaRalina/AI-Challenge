package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aiwork/backend/internal/activity"
)

// Day 19: the composition pipeline. list_events (already day 18) feeds
// build_sessions, a pure function with no I/O — given the events, it groups
// them into WorkSession blocks; save_sessions then persists the result.
// Kept as three separate tools (not one), exactly mirroring the challenge's
// search → summarize → saveToFile shape, so the chain and the data passed
// between its steps are both real MCP calls, inspectable independently.

const (
	// sessionGap: events more than this far apart start a new session.
	sessionGap = 45 * time.Minute
	// sessionLeadIn: a session is assumed to start this long before its
	// first event — time spent reading/thinking before the first commit.
	sessionLeadIn = 30 * time.Minute
)

// Category buckets a session's events for analytics — no finer than this
// (no "focus"/"idle"/etc.): a session is development or review depending on
// which kind of event dominates it; "other" is reserved for future event
// kinds (e.g. calendar meetings, day 20) that are neither.
type Category string

const (
	CategoryDevelopment Category = "development"
	CategoryReview      Category = "review"
	CategoryOther       Category = "other"
)

func categoryOf(kind activity.Kind) Category {
	switch kind {
	case activity.KindCommit, activity.KindPROpened, activity.KindPRMerged:
		return CategoryDevelopment
	case activity.KindReview, activity.KindIssueComment:
		return CategoryReview
	default:
		return CategoryOther
	}
}

// WorkSession is one contiguous block of work, reconstructed from events
// that are all within sessionGap of their neighbor. Category and Repo are
// the session's dominant one — a session touching two repos or mixing a
// commit with a review still gets exactly one of each, so every session
// contributes its whole duration to exactly one bar in the by-day/by-project
// charts, never split.
type WorkSession struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Category     Category  `json:"category" jsonschema:"development, review, or other — whichever kind of event dominates the session"`
	Repo         string    `json:"repo" jsonschema:"the session's dominant repository"`
	EventCount   int       `json:"event_count"`
	FirstEventID string    `json:"first_event_id"`
	LastEventID  string    `json:"last_event_id"`
}

// buildSessions groups events (any order) into WorkSessions. Pure: same
// input always yields the same output, no clock, no I/O — the property that
// makes it safe to call as its own MCP tool ahead of save_sessions.
func buildSessions(events []activity.Event) []WorkSession {
	if len(events) == 0 {
		return []WorkSession{}
	}
	sorted := make([]activity.Event, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OccurredAt.Before(sorted[j].OccurredAt) })

	var sessions []WorkSession
	b := newSessionBuilder(sorted[0])
	for _, e := range sorted[1:] {
		if e.OccurredAt.Sub(b.lastAt) > sessionGap {
			sessions = append(sessions, b.finish())
			b = newSessionBuilder(e)
			continue
		}
		b.add(e)
	}
	sessions = append(sessions, b.finish())
	return sessions
}

// sessionBuilder accumulates one session's events. Repo/category are picked
// by count with ties going to whichever was seen first, so a single
// dominant repo or category never flips on a coin toss.
type sessionBuilder struct {
	firstAt, lastAt           time.Time
	firstEventID, lastEventID string
	eventCount                int
	repoOrder                 []string
	repoCounts                map[string]int
	categoryOrder             []Category
	categoryCounts            map[Category]int
}

func newSessionBuilder(e activity.Event) *sessionBuilder {
	b := &sessionBuilder{
		firstAt: e.OccurredAt, lastAt: e.OccurredAt,
		firstEventID: e.ID, lastEventID: e.ID,
		repoCounts: map[string]int{}, categoryCounts: map[Category]int{},
	}
	b.add(e)
	return b
}

func (b *sessionBuilder) add(e activity.Event) {
	b.lastAt = e.OccurredAt
	b.lastEventID = e.ID
	b.eventCount++
	if b.repoCounts[e.Repo] == 0 {
		b.repoOrder = append(b.repoOrder, e.Repo)
	}
	b.repoCounts[e.Repo]++
	cat := categoryOf(e.Kind)
	if b.categoryCounts[cat] == 0 {
		b.categoryOrder = append(b.categoryOrder, cat)
	}
	b.categoryCounts[cat]++
}

func (b *sessionBuilder) finish() WorkSession {
	repo := b.repoOrder[0]
	for _, r := range b.repoOrder[1:] {
		if b.repoCounts[r] > b.repoCounts[repo] {
			repo = r
		}
	}
	category := b.categoryOrder[0]
	for _, c := range b.categoryOrder[1:] {
		if b.categoryCounts[c] > b.categoryCounts[category] {
			category = c
		}
	}
	return WorkSession{
		Start: b.firstAt.Add(-sessionLeadIn), End: b.lastAt,
		Category: category, Repo: repo, EventCount: b.eventCount,
		FirstEventID: b.firstEventID, LastEventID: b.lastEventID,
	}
}

// ---- build_sessions ----

type BuildSessionsInput struct {
	Events []activity.Event `json:"events" jsonschema:"events to group, any order — typically list_events' full history (all: true)"`
}

type BuildSessionsOutput struct {
	Sessions    []WorkSession `json:"sessions"`
	EventsIn    int           `json:"events_in"`
	SessionsOut int           `json:"sessions_out"`
}

func (s *server) buildSessionsTool(_ context.Context, _ *mcp.CallToolRequest, in BuildSessionsInput) (*mcp.CallToolResult, BuildSessionsOutput, error) {
	sessions := buildSessions(in.Events)
	return nil, BuildSessionsOutput{Sessions: sessions, EventsIn: len(in.Events), SessionsOut: len(sessions)}, nil
}

// ---- save_sessions ----

type SaveSessionsInput struct {
	Sessions []WorkSession `json:"sessions" jsonschema:"the full set of sessions to store — replaces whatever was stored before"`
}

type SaveSessionsOutput struct {
	Saved    int `json:"saved"`
	Replaced int `json:"replaced" jsonschema:"how many sessions were stored before this call, now discarded"`
}

func (s *server) saveSessionsTool(ctx context.Context, _ *mcp.CallToolRequest, in SaveSessionsInput) (*mcp.CallToolResult, SaveSessionsOutput, error) {
	rows := make([]storedSession, len(in.Sessions))
	for i, sess := range in.Sessions {
		if !sess.End.After(sess.Start) {
			return nil, SaveSessionsOutput{}, fmt.Errorf("sessions[%d]: end must be after start", i)
		}
		rows[i] = storedSession{
			startAt: sess.Start.Unix(), endAt: sess.End.Unix(),
			category: string(sess.Category), repo: sess.Repo, eventCount: sess.EventCount,
			firstEventID: sess.FirstEventID, lastEventID: sess.LastEventID,
		}
	}
	previous, err := s.store.replaceSessions(ctx, rows)
	if err != nil {
		return nil, SaveSessionsOutput{}, fmt.Errorf("store: %w", err)
	}
	return nil, SaveSessionsOutput{Saved: len(rows), Replaced: previous}, nil
}

// ---- get_sessions ----

type GetSessionsInput struct {
	From    string   `json:"from" jsonschema:"start of the period: RFC3339 timestamp, or a YYYY-MM-DD date (local time)"`
	To      string   `json:"to,omitempty" jsonschema:"end of the period; defaults to now"`
	Repos   []string `json:"repos,omitempty" jsonschema:"only sessions whose dominant repository is one of these"`
	Project string   `json:"project,omitempty" jsonschema:"only sessions mapped to this project name; \"Без проекта\" for unmapped repositories"`
}

type SessionView struct {
	WorkSession
	Project string `json:"project"`
}

type GetSessionsOutput struct {
	From     time.Time     `json:"from"`
	To       time.Time     `json:"to"`
	Timezone string        `json:"timezone"`
	Sessions []SessionView `json:"sessions"`
	Total    int           `json:"total"`
	Warnings []string      `json:"warnings,omitempty"`
}

func (s *server) getSessions(ctx context.Context, _ *mcp.CallToolRequest, in GetSessionsInput) (*mcp.CallToolResult, GetSessionsOutput, error) {
	f, warnings, err := s.resolveSessionFilter(ctx, in.From, in.To, in.Repos, in.Project, maxDigestWindow)
	if err != nil {
		return nil, GetSessionsOutput{}, err
	}
	rows, err := s.store.sessions(ctx, f)
	if err != nil {
		return nil, GetSessionsOutput{}, err
	}
	out := GetSessionsOutput{From: f.from.In(time.Local), To: f.to.In(time.Local), Timezone: activity.LocalZoneName(), Warnings: warnings, Total: len(rows), Sessions: make([]SessionView, len(rows))}
	for i, row := range rows {
		out.Sessions[i] = sessionViewFrom(row)
	}
	return nil, out, nil
}

func sessionViewFrom(row storedSessionRow) SessionView {
	return SessionView{
		WorkSession: WorkSession{
			Start: time.Unix(row.startAt, 0).In(time.Local), End: time.Unix(row.endAt, 0).In(time.Local),
			Category: Category(row.category), Repo: row.repo, EventCount: row.eventCount,
			FirstEventID: row.firstEventID, LastEventID: row.lastEventID,
		},
		Project: row.project,
	}
}

// resolveSessionFilter is get_sessions' and get_analytics' shared period/
// repo/project resolution — the sessions-table analogue of resolvePeriod.
func (s *server) resolveSessionFilter(ctx context.Context, fromStr, toStr string, repos []string, project string, maxWindow time.Duration) (sessionFilter, []string, error) {
	var f sessionFilter
	now := time.Now()
	from, err := activity.ParseBound(fromStr, false)
	if err != nil {
		return f, nil, fmt.Errorf("from: %w", err)
	}
	to := now
	if toStr != "" {
		if to, err = activity.ParseBound(toStr, true); err != nil {
			return f, nil, fmt.Errorf("to: %w", err)
		}
	}
	if to.After(now) {
		to = now
	}
	if !to.After(from) {
		return f, nil, errors.New("to must be after from")
	}
	if to.Sub(from) > maxWindow {
		return f, nil, fmt.Errorf("the period must not exceed %d days", int(maxWindow.Hours()/24))
	}
	f = sessionFilter{from: from, to: to, project: project}
	var warnings []string
	if len(repos) > 0 {
		known, err := s.store.repoNames(ctx)
		if err != nil {
			return f, nil, err
		}
		var unmatched []string
		f.repos, unmatched = matchRepos(known, repos)
		warnings = append(warnings, unmatched...)
		if len(f.repos) == 0 {
			f.repos = repos
		}
	}
	return f, warnings, nil
}

// ---- set_repo_project / get_repo_projects ----

type SetRepoProjectInput struct {
	Repo    string `json:"repo" jsonschema:"owner/repo, as it appears in stored events"`
	Project string `json:"project" jsonschema:"project name to group this repo's sessions under; empty clears the mapping (falls back to \"Без проекта\")"`
}

type SetRepoProjectOutput struct {
	Repo    string `json:"repo"`
	Project string `json:"project,omitempty"`
}

func (s *server) setRepoProjectTool(ctx context.Context, _ *mcp.CallToolRequest, in SetRepoProjectInput) (*mcp.CallToolResult, SetRepoProjectOutput, error) {
	repo := in.Repo
	if repo == "" {
		return nil, SetRepoProjectOutput{}, errors.New("repo is required")
	}
	if err := s.store.setRepoProject(ctx, repo, in.Project); err != nil {
		return nil, SetRepoProjectOutput{}, fmt.Errorf("store: %w", err)
	}
	return nil, SetRepoProjectOutput{Repo: repo, Project: in.Project}, nil
}

type GetRepoProjectsInput struct{}

type RepoProjectView struct {
	Repo    string `json:"repo"`
	Project string `json:"project,omitempty" jsonschema:"empty means unmapped (\"Без проекта\")"`
}

type GetRepoProjectsOutput struct {
	Repos []RepoProjectView `json:"repos"`
}

func (s *server) getRepoProjectsTool(ctx context.Context, _ *mcp.CallToolRequest, _ GetRepoProjectsInput) (*mcp.CallToolResult, GetRepoProjectsOutput, error) {
	rows, err := s.store.repoProjects(ctx)
	if err != nil {
		return nil, GetRepoProjectsOutput{}, err
	}
	out := GetRepoProjectsOutput{Repos: make([]RepoProjectView, len(rows))}
	for i, r := range rows {
		out.Repos[i] = RepoProjectView{Repo: r.repo, Project: r.project}
	}
	return nil, out, nil
}
