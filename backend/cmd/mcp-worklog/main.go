// Command mcp-worklog is the product's own Worklog MCP server: the internal
// store of the user's work history. Adapters such as mcp-github fetch
// activity; this server keeps it (SQLite) and answers questions about it
// without going back to the source. It never calls other MCP servers — the
// backend's background collector moves events from an adapter into it.
//
// Tools:
//
//   - ingest_events       — store a batch of events (dedup by id) and advance
//     the source's sync cursor
//   - get_sync_state      — per-source covered range, the collector's cursor
//   - list_events         — stored events for a period, newest first
//   - get_activity_digest — aggregated counts for a period: by kind,
//     repository and day
//
// Day 19 adds the composition pipeline that turns events into work sessions,
// and the analytics read side over them:
//
//   - build_sessions      — pure: groups events into WorkSession blocks,
//     independently per repository
//   - save_sessions       — replaces the stored sessions with a new set
//   - get_sessions        — stored sessions for a period
//   - set_repo_project    — opt in to merging a repository's hours under a
//     shared project label (every repo stands on its own by default)
//   - get_repo_projects   — every known repository and its mapping
//   - get_analytics       — KPIs, time by project/day, a commit-type
//     breakdown, a weekday×hour heatmap, and an 8-week trend
//
// Day 20 adds meetings (kind=meeting events, from mcp-calendar) into the
// same events/sessions tables, and one more read tool:
//
//   - get_day_timeline    — one day as non-overlapping work/meeting blocks
//
// It speaks MCP over stdio. WORKLOG_DB is the database file path (created if
// missing). Stdout carries the protocol, so all logging goes to stderr.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aiwork/backend/internal/activity"
)

const (
	defaultListLimit = 100
	maxListLimit     = 500
	maxIngestEvents  = 2000
	maxDigestWindow  = 366 * 24 * time.Hour
	// maxDigestDays is how long a period may be for by_day to list every
	// day, empty ones included; longer periods list only active days.
	maxDigestDays = 62
)

var validKinds = map[activity.Kind]bool{
	activity.KindCommit: true, activity.KindPROpened: true, activity.KindPRMerged: true,
	activity.KindReview: true, activity.KindIssueComment: true, activity.KindMeeting: true,
}

type server struct {
	store *store
	path  string
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("mcp-worklog: ")
	_ = godotenv.Load()

	path := strings.TrimSpace(os.Getenv("WORKLOG_DB"))
	if path == "" {
		path = filepath.Join("data", "worklog.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatalf("cannot create %s: %v", filepath.Dir(path), err)
	}
	st, err := openStore(path)
	if err != nil {
		log.Fatalf("cannot open %s: %v", path, err)
	}
	defer st.Close()
	s := &server{store: st, path: path}

	srv := mcp.NewServer(&mcp.Implementation{Name: "aiwork-worklog", Title: "Worklog", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: "The user's stored work history: activity events collected in the background from their sources (GitHub), with a per-source sync cursor, and aggregated digests over it.",
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	notDestructive := false

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ingest_events",
		Title:       "Сохранить события",
		Description: "Store a batch of activity events fetched from one source for the window [window_since, window_until]. Events already stored (same source and id) are skipped, so overlapping windows are safe. With replace, every stored event of this source inside the window is deleted first, so a moved/renamed/deleted item (e.g. a rescheduled calendar meeting) doesn't linger — use it for a source whose events can change after the fact. Advances the source's sync cursor in the same transaction. Used by the background collector, not for answering questions.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &notDestructive},
	}, s.ingestEvents)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_sync_state",
		Title:       "Состояние синхронизации",
		Description: "Per source: the continuously collected range (synced_since..synced_until — the collector's cursor), plus the total number of stored events.",
		Annotations: readOnly,
	}, s.getSyncState)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_events",
		Title:       "События из журнала",
		Description: "List the user's stored work activity events (commits, PRs opened and merged, reviews, comments) for a period, newest first, with per-kind counts over all matches. Answers from the local work history — fast, no call to GitHub. The result says which range is collected (coverage); events after synced_until are not collected yet.",
		Annotations: readOnly,
	}, s.listEvents)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_activity_digest",
		Title:       "Сводка активности",
		Description: "Aggregated summary of the user's stored work activity for a period: total, counts per kind, per repository and per day, active days, meetings_count, first and last event. Meetings are counted in total/counts/active_days/meetings_count but excluded from by_repo (they have no repository). Answers from the local work history — fast, no call to GitHub or the calendar. Use it for 'how much / which repos / which days' questions; use list_events when the actual items are needed.",
		Annotations: readOnly,
	}, s.getDigest)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "build_sessions",
		Title:       "Собрать сессии из событий",
		Description: "Pure computation, no storage: groups activity events into sessions. Commit/PR/review/comment events become work sessions, independently per repository — contiguous blocks where consecutive events in the same repository are no more than 45 minutes apart, each starting 30 minutes before its first event; a session never spans two repositories, so working in several repos in the same window still counts every one of them in full. Meeting events (kind=meeting, from the Calendar server) each become their own meeting session directly, using their own start/end — no gap-merging, since a calendar event already has exact bounds. Does not read or write the database; pass it save_sessions' input to persist the result.",
		Annotations: readOnly,
	}, s.buildSessionsTool)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "save_sessions",
		Title:       "Сохранить сессии",
		Description: "Replaces every stored work session with the given set — sessions are derived from events, not authoritative facts, so a full pipeline run (list_events -> build_sessions -> save_sessions) recomputes and replaces them wholesale rather than merging. Used by the background collector, not for answering questions.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &notDestructive},
	}, s.saveSessionsTool)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_sessions",
		Title:       "Сессии из журнала",
		Description: "List the user's stored sessions for a period: work sessions (repository, event count, project — the repository's own bare name unless explicitly mapped to something else) and meeting sessions (title) side by side, distinguished by kind. Answers from the local work history — no call to GitHub or the calendar.",
		Annotations: readOnly,
	}, s.getSessions)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "set_repo_project",
		Title:       "Привязать репозиторий к проекту",
		Description: "Every repository is shown on its own in analytics by default (project = its bare name) — this tool is opt-in, for merging several repositories under one shared project label. Takes effect immediately for every future query — sessions are not recomputed, since the project is resolved at read time, not stored on the session.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &notDestructive},
	}, s.setRepoProjectTool)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_repo_projects",
		Title:       "Репозитории и их проекты",
		Description: "Every repository with at least one stored event, and its explicit project mapping if any (unmapped repositories use their own bare name as the project everywhere else). Used to build a repo -> project mapping UI.",
		Annotations: readOnly,
	}, s.getRepoProjectsTool)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_analytics",
		Title:       "Аналитика по сессиям",
		Description: "Everything the Analytics screen's charts need for a period, computed from stored sessions: KPIs (total and meeting hours, active days, work sessions, meetings, repos, projects), hours by project (a repository with no explicit project mapping is its own project), hours by day (split into work/meeting/total), a commit-type breakdown (feat/fix/chore/... parsed from commit messages, by count), a weekday×hour heatmap of work only, and an 8-week trend (always the trailing 8 weeks, independent of the requested period). Meetings take priority over work wherever they overlap, so time is never double-counted. No LLM, no productivity score — counts and hours only.",
		Annotations: readOnly,
	}, s.getAnalytics)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_day_timeline",
		Title:       "Таймлайн дня",
		Description: "One day laid out as non-overlapping blocks — work (repository, project) or meeting (title) — ready to draw as a Gantt-style day view, or to answer 'what did I do on <day>' from a single call instead of combining get_sessions results by hand. A meeting always wins any time it shares with work.",
		Annotations: readOnly,
	}, s.getDayTimeline)

	log.Printf("database %s", path)
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

// ---- ingest_events ----

type IngestInput struct {
	Source      string           `json:"source" jsonschema:"source the events come from, e.g. github"`
	Events      []activity.Event `json:"events" jsonschema:"events fetched for the window; each must have this source"`
	WindowSince string           `json:"window_since" jsonschema:"start of the fetched window (RFC3339)"`
	WindowUntil string           `json:"window_until" jsonschema:"end of the fetched window (RFC3339); becomes the sync cursor"`
	Replace     bool             `json:"replace,omitempty" jsonschema:"for a source whose events can change after the fact (e.g. calendar: a meeting can be moved, renamed or deleted) — deletes every stored event of this source inside [window_since, window_until] before inserting, so the window ends up matching exactly what was just fetched, with no stale leftovers. Leave false for an immutable source like GitHub, where dedup-by-id is enough and nothing is ever removed from history."`
}

type IngestOutput struct {
	Received   int       `json:"received"`
	Inserted   int       `json:"inserted" jsonschema:"events that were new"`
	Duplicates int       `json:"duplicates" jsonschema:"events already stored, skipped"`
	Deleted    int       `json:"deleted,omitempty" jsonschema:"stored events removed from the window because replace was true and the source no longer reports them"`
	Sync       SyncState `json:"sync" jsonschema:"the source's covered range after this batch"`
}

func (s *server) ingestEvents(ctx context.Context, _ *mcp.CallToolRequest, in IngestInput) (*mcp.CallToolResult, IngestOutput, error) {
	source := strings.TrimSpace(in.Source)
	if source == "" {
		return nil, IngestOutput{}, errors.New("source is required")
	}
	since, err := time.Parse(time.RFC3339, in.WindowSince)
	if err != nil {
		return nil, IngestOutput{}, fmt.Errorf("window_since: %w", err)
	}
	until, err := time.Parse(time.RFC3339, in.WindowUntil)
	if err != nil {
		return nil, IngestOutput{}, fmt.Errorf("window_until: %w", err)
	}
	if !until.After(since) {
		return nil, IngestOutput{}, errors.New("window_until must be after window_since")
	}
	if len(in.Events) > maxIngestEvents {
		return nil, IngestOutput{}, fmt.Errorf("at most %d events per batch", maxIngestEvents)
	}
	for i, e := range in.Events {
		switch {
		case e.ID == "":
			return nil, IngestOutput{}, fmt.Errorf("events[%d]: id is required", i)
		case e.Source != source:
			return nil, IngestOutput{}, fmt.Errorf("events[%d]: source %q, expected %q", i, e.Source, source)
		case !validKinds[e.Kind]:
			return nil, IngestOutput{}, fmt.Errorf("events[%d]: unknown kind %q", i, e.Kind)
		case e.OccurredAt.IsZero():
			return nil, IngestOutput{}, fmt.Errorf("events[%d]: occurred_at is required", i)
		}
	}

	res, err := s.store.ingest(ctx, source, in.Events, since, until, in.Replace)
	if err != nil {
		return nil, IngestOutput{}, fmt.Errorf("store: %w", err)
	}
	log.Printf("ingest %s %s..%s: %d received, %d new, %d duplicate(s), %d deleted",
		source, since.Format(time.RFC3339), until.Format(time.RFC3339), len(in.Events), res.inserted, res.duplicates, res.deleted)
	return nil, IngestOutput{Received: len(in.Events), Inserted: res.inserted, Duplicates: res.duplicates, Deleted: res.deleted, Sync: res.state}, nil
}

// ---- get_sync_state ----

type SyncStateInput struct{}

type SyncStateOutput struct {
	Sources     []SyncState `json:"sources" jsonschema:"one entry per source that has been collected at least once"`
	TotalEvents int         `json:"total_events"`
	Database    string      `json:"database" jsonschema:"database file path"`
}

func (s *server) getSyncState(ctx context.Context, _ *mcp.CallToolRequest, _ SyncStateInput) (*mcp.CallToolResult, SyncStateOutput, error) {
	states, err := s.store.syncStates(ctx)
	if err != nil {
		return nil, SyncStateOutput{}, err
	}
	total, err := s.store.countEvents(ctx)
	if err != nil {
		return nil, SyncStateOutput{}, err
	}
	return nil, SyncStateOutput{Sources: states, TotalEvents: total, Database: s.path}, nil
}

// ---- shared period handling ----

// periodInput is the period and filters list_events and get_activity_digest
// share.
type periodInput struct {
	From  string          `json:"from" jsonschema:"start of the period: RFC3339 timestamp, or a YYYY-MM-DD date (start of that day, local time)"`
	To    string          `json:"to,omitempty" jsonschema:"end of the period: RFC3339 timestamp, or a YYYY-MM-DD date (end of that day, local time); defaults to now"`
	Repos []string        `json:"repos,omitempty" jsonschema:"only these repositories (owner/repo or the bare repo name)"`
	Kinds []activity.Kind `json:"kinds,omitempty" jsonschema:"only these kinds: commit, pr_opened, pr_merged, review, issue_comment"`
}

// periodMeta is what every period answer carries besides its data: the
// exact period, and how much of it the worklog has actually collected.
type periodMeta struct {
	From     time.Time   `json:"from"`
	To       time.Time   `json:"to"`
	Timezone string      `json:"timezone" jsonschema:"local timezone all times and days are in"`
	Coverage []SyncState `json:"coverage" jsonschema:"collected range per source"`
	Covered  bool        `json:"covered" jsonschema:"true when the whole period lies inside the collected range"`
	Warnings []string    `json:"warnings,omitempty"`
}

func (s *server) resolvePeriod(ctx context.Context, in periodInput, maxWindow time.Duration) (eventFilter, periodMeta, error) {
	var f eventFilter
	var meta periodMeta
	now := time.Now()
	from, err := activity.ParseBound(in.From, false)
	if err != nil {
		return f, meta, fmt.Errorf("from: %w", err)
	}
	to := now
	if in.To != "" {
		if to, err = activity.ParseBound(in.To, true); err != nil {
			return f, meta, fmt.Errorf("to: %w", err)
		}
	}
	if to.After(now) {
		to = now
	}
	if !to.After(from) {
		return f, meta, errors.New("to must be after from")
	}
	if to.Sub(from) > maxWindow {
		return f, meta, fmt.Errorf("the period must not exceed %d days", int(maxWindow.Hours()/24))
	}
	for _, k := range in.Kinds {
		if !validKinds[k] {
			return f, meta, fmt.Errorf("unknown kind %q", k)
		}
	}
	f = eventFilter{from: from, to: to, kinds: in.Kinds}
	meta = periodMeta{From: from.In(time.Local), To: to.In(time.Local), Timezone: activity.LocalZoneName(), Coverage: []SyncState{}}

	if len(in.Repos) > 0 {
		known, err := s.store.repoNames(ctx)
		if err != nil {
			return f, meta, err
		}
		var unmatched []string
		f.repos, unmatched = matchRepos(known, in.Repos)
		meta.Warnings = append(meta.Warnings, unmatched...)
		if len(f.repos) == 0 {
			// Nothing matched: filter on the names as given so the answer
			// is an honest "no events", not every repository.
			f.repos = in.Repos
		}
	}

	states, err := s.store.syncStates(ctx)
	if err != nil {
		return f, meta, err
	}
	meta.Coverage = states
	meta.Covered = len(states) > 0
	for _, st := range states {
		if st.SyncedSince.After(from) || st.SyncedUntil.Before(to) {
			meta.Covered = false
			meta.Warnings = append(meta.Warnings, fmt.Sprintf("%s: в журнале собраны события только за %s — %s; остальная часть периода не собрана",
				st.Source, st.SyncedSince.Format("02.01.2006 15:04"), st.SyncedUntil.Format("02.01.2006 15:04")))
		}
	}
	if len(states) == 0 {
		meta.Warnings = append(meta.Warnings, "журнал пуст: сбор активности ещё ни разу не выполнялся")
	}
	return f, meta, nil
}

// matchRepos resolves requested names against stored repositories: full
// owner/repo, or a bare repo name when it's unambiguous.
func matchRepos(known, names []string) (matched, warnings []string) {
	picked := map[string]bool{}
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		var hits []string
		for _, r := range known {
			full := strings.ToLower(r)
			if full == name || (!strings.Contains(name, "/") && strings.HasSuffix(full, "/"+name)) {
				hits = append(hits, r)
			}
		}
		switch {
		case len(hits) == 0:
			warnings = append(warnings, fmt.Sprintf("%s: в журнале нет событий из этого репозитория", raw))
		case len(hits) > 1:
			warnings = append(warnings, fmt.Sprintf("%s: неоднозначно, укажите owner/repo", raw))
		case !picked[hits[0]]:
			picked[hits[0]] = true
			matched = append(matched, hits[0])
		}
	}
	return matched, warnings
}

// ---- list_events ----

type ListEventsInput struct {
	periodInput
	Limit int  `json:"limit,omitempty" jsonschema:"maximum number of events to return, newest first (default 100, max 500); ignored when all is true"`
	All   bool `json:"all,omitempty" jsonschema:"return every matching event, no limit — for the session-building pipeline, not for answering questions (it can be a lot of text)"`
}

type ListEventsOutput struct {
	periodMeta
	Events    []activity.Event      `json:"events" jsonschema:"events, newest first"`
	Total     int                   `json:"total" jsonschema:"number of matching events (may exceed the events returned)"`
	Counts    map[activity.Kind]int `json:"counts" jsonschema:"number of matching events per kind"`
	Truncated bool                  `json:"truncated" jsonschema:"true when total exceeds the events returned"`
}

func (s *server) listEvents(ctx context.Context, _ *mcp.CallToolRequest, in ListEventsInput) (*mcp.CallToolResult, ListEventsOutput, error) {
	// The window cap protects a chat answer from an accidentally huge
	// question; the session-building pipeline explicitly wants the entire
	// history, so all:true also lifts it.
	windowCap := maxDigestWindow
	if in.All {
		windowCap = 100 * 365 * 24 * time.Hour
	}
	f, meta, err := s.resolvePeriod(ctx, in.periodInput, windowCap)
	if err != nil {
		return nil, ListEventsOutput{}, err
	}
	limit := in.Limit
	if in.All {
		limit = 0
	} else {
		if limit <= 0 {
			limit = defaultListLimit
		}
		limit = min(limit, maxListLimit)
	}
	events, err := s.store.events(ctx, f, limit)
	if err != nil {
		return nil, ListEventsOutput{}, err
	}
	counts, total, err := s.store.countByKind(ctx, f)
	if err != nil {
		return nil, ListEventsOutput{}, err
	}
	return nil, ListEventsOutput{periodMeta: meta, Events: events, Total: total, Counts: counts, Truncated: total > len(events)}, nil
}

// ---- get_activity_digest ----

type DigestInput struct {
	periodInput
}

type RepoDigest struct {
	Repo   string                `json:"repo"`
	Total  int                   `json:"total"`
	Counts map[activity.Kind]int `json:"counts"`
}

type DayDigest struct {
	Date    string                `json:"date" jsonschema:"YYYY-MM-DD, local time"`
	Weekday string                `json:"weekday"`
	Total   int                   `json:"total"`
	Counts  map[activity.Kind]int `json:"counts"`
}

type DigestOutput struct {
	periodMeta
	Total         int                   `json:"total"`
	Counts        map[activity.Kind]int `json:"counts" jsonschema:"events per kind"`
	ByRepo        []RepoDigest          `json:"by_repo" jsonschema:"per repository, most active first; meetings excluded (they have no repository) — see meetings_count"`
	ByDay         []DayDigest           `json:"by_day" jsonschema:"per day, oldest first; every day of the period when it is at most 62 days long, otherwise only days with events"`
	ActiveDays    int                   `json:"active_days" jsonschema:"days with at least one event, GitHub or meeting"`
	MeetingsCount int                   `json:"meetings_count" jsonschema:"same as counts.meeting, surfaced as its own field since a meeting is a different kind of thing from a repository event"`
	FirstEventAt  *time.Time            `json:"first_event_at,omitempty"`
	LastEventAt   *time.Time            `json:"last_event_at,omitempty"`
}

var weekdays = [...]string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}

func (s *server) getDigest(ctx context.Context, _ *mcp.CallToolRequest, in DigestInput) (*mcp.CallToolResult, DigestOutput, error) {
	f, meta, err := s.resolvePeriod(ctx, in.periodInput, maxDigestWindow)
	if err != nil {
		return nil, DigestOutput{}, err
	}
	events, err := s.store.events(ctx, f, 0)
	if err != nil {
		return nil, DigestOutput{}, err
	}

	out := DigestOutput{periodMeta: meta, Total: len(events), Counts: map[activity.Kind]int{}, ByRepo: []RepoDigest{}, ByDay: []DayDigest{}}
	repos := map[string]*RepoDigest{}
	days := map[string]*DayDigest{}
	for _, e := range events { // newest first
		out.Counts[e.Kind]++
		// A meeting has no repository (e.Repo == "") — grouping it in would
		// produce a nameless, meaningless "repo" entry; meetings_count is
		// its own field instead (see DigestOutput).
		if e.Kind != activity.KindMeeting {
			r := repos[e.Repo]
			if r == nil {
				r = &RepoDigest{Repo: e.Repo, Counts: map[activity.Kind]int{}}
				repos[e.Repo] = r
			}
			r.Total++
			r.Counts[e.Kind]++
		}
		key := e.OccurredAt.Format("2006-01-02")
		d := days[key]
		if d == nil {
			d = &DayDigest{Date: key, Weekday: weekdays[e.OccurredAt.Weekday()], Counts: map[activity.Kind]int{}}
			days[key] = d
		}
		d.Total++
		d.Counts[e.Kind]++
	}
	if len(events) > 0 {
		first, last := events[len(events)-1].OccurredAt, events[0].OccurredAt
		out.FirstEventAt, out.LastEventAt = &first, &last
	}
	out.ActiveDays = len(days)
	out.MeetingsCount = out.Counts[activity.KindMeeting]

	for _, r := range repos {
		out.ByRepo = append(out.ByRepo, *r)
	}
	sort.Slice(out.ByRepo, func(i, j int) bool {
		if out.ByRepo[i].Total != out.ByRepo[j].Total {
			return out.ByRepo[i].Total > out.ByRepo[j].Total
		}
		return out.ByRepo[i].Repo < out.ByRepo[j].Repo
	})

	start := meta.From
	dayStart := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
	if meta.To.Sub(dayStart) <= maxDigestDays*24*time.Hour {
		for d := dayStart; !d.After(meta.To); d = d.AddDate(0, 0, 1) {
			key := d.Format("2006-01-02")
			if day := days[key]; day != nil {
				out.ByDay = append(out.ByDay, *day)
			} else {
				out.ByDay = append(out.ByDay, DayDigest{Date: key, Weekday: weekdays[d.Weekday()], Counts: map[activity.Kind]int{}})
			}
		}
	} else {
		for _, d := range days {
			out.ByDay = append(out.ByDay, *d)
		}
		sort.Slice(out.ByDay, func(i, j int) bool { return out.ByDay[i].Date < out.ByDay[j].Date })
	}
	return nil, out, nil
}
