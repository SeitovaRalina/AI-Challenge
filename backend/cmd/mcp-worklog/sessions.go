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
//
// Sessions carry no development/review split: for a workflow that's mostly
// commits (reviews and comments are rare in a solo/small-team repo), that
// split was almost always 100% development and told the user nothing. What
// the user's own commit messages already encode — a conventional-commit
// type prefix (feat/fix/chore/...) — is a real signal instead, surfaced by
// get_analytics as a commit-type breakdown (analytics.go), not baked into
// the session model here.

const (
	// sessionGap: events more than this far apart start a new session.
	sessionGap = 45 * time.Minute
	// sessionLeadIn: a session is assumed to start this long before its
	// first event — time spent reading/thinking before the first commit.
	sessionLeadIn = 30 * time.Minute
)

// WorkSession is one contiguous block of work in a single repository,
// reconstructed from that repository's events that are all within
// sessionGap of their neighbor. A session never spans more than one
// repository — working in two repos in the same 45-minute window produces
// two overlapping sessions, one per repo, each counted in full, rather than
// one session that silently drops whichever repo had fewer events.
type WorkSession struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Repo         string    `json:"repo"`
	EventCount   int       `json:"event_count"`
	FirstEventID string    `json:"first_event_id"`
	LastEventID  string    `json:"last_event_id"`
}

// buildSessions groups events (any order) into WorkSessions, independently
// per repository (see WorkSession's doc for why). Pure: same input always
// yields the same output, no clock, no I/O — the property that makes it
// safe to call as its own MCP tool ahead of save_sessions.
func buildSessions(events []activity.Event) []WorkSession {
	byRepo := map[string][]activity.Event{}
	for _, e := range events {
		byRepo[e.Repo] = append(byRepo[e.Repo], e)
	}
	repos := make([]string, 0, len(byRepo))
	for repo := range byRepo {
		repos = append(repos, repo)
	}
	sort.Strings(repos) // deterministic regardless of map iteration order

	var sessions []WorkSession
	for _, repo := range repos {
		sessions = append(sessions, buildSessionsForRepo(repo, byRepo[repo])...)
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].Start.Before(sessions[j].Start) })
	if sessions == nil {
		sessions = []WorkSession{}
	}
	return sessions
}

// buildSessionsForRepo groups one repository's events by time gap alone.
func buildSessionsForRepo(repo string, events []activity.Event) []WorkSession {
	sorted := make([]activity.Event, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OccurredAt.Before(sorted[j].OccurredAt) })

	var sessions []WorkSession
	b := newSessionBuilder(repo, sorted[0])
	for _, e := range sorted[1:] {
		if e.OccurredAt.Sub(b.lastAt) > sessionGap {
			sessions = append(sessions, b.finish())
			b = newSessionBuilder(repo, e)
			continue
		}
		b.add(e)
	}
	sessions = append(sessions, b.finish())
	return sessions
}

// sessionBuilder accumulates one repository's session.
type sessionBuilder struct {
	repo                      string
	firstAt, lastAt           time.Time
	firstEventID, lastEventID string
	eventCount                int
}

func newSessionBuilder(repo string, e activity.Event) *sessionBuilder {
	b := &sessionBuilder{repo: repo, firstAt: e.OccurredAt, lastAt: e.OccurredAt, firstEventID: e.ID, lastEventID: e.ID}
	b.add(e)
	return b
}

func (b *sessionBuilder) add(e activity.Event) {
	b.lastAt = e.OccurredAt
	b.lastEventID = e.ID
	b.eventCount++
}

func (b *sessionBuilder) finish() WorkSession {
	return WorkSession{
		Start: b.firstAt.Add(-sessionLeadIn), End: b.lastAt,
		Repo: b.repo, EventCount: b.eventCount,
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
			repo: sess.Repo, eventCount: sess.EventCount,
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
	Repos   []string `json:"repos,omitempty" jsonschema:"only sessions in one of these repositories"`
	Project string   `json:"project,omitempty" jsonschema:"only sessions explicitly mapped to this project name via set_repo_project (an unmapped repository's own name is not matched here — filter by repos for that)"`
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
			Repo: row.repo, EventCount: row.eventCount,
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
	Project string `json:"project" jsonschema:"project label to merge this repo's hours under in analytics (e.g. to combine several repos into one project); empty clears the mapping, falling back to the repository's own bare name — every repository is shown on its own by default, this is opt-in merging, not a prerequisite"`
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
	Project string `json:"project,omitempty" jsonschema:"empty means not explicitly mapped — the repository's own bare name is used as its project label everywhere else"`
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
