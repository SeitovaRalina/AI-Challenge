package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aiwork/backend/internal/mcpclient"
)

// Day 18: the background activity collector. The backend (the MCP host) runs
// it on a schedule, chaining the product's own MCP servers:
//
//	worklog.get_sync_state                  where did the last run stop?
//	github.get_activity (per ≤7-day chunk)  fetch what happened since
//	worklog.ingest_events                   store it (dedup by id), advance the cursor
//	worklog.get_activity_digest ×2          aggregate: today and the last 7 days
//
// The cursor lives in the worklog database, committed together with the
// events, so a restart simply picks up from it. Each run re-fetches the last
// collectOverlap before the cursor: a commit's date is when it was authored,
// and it can be pushed (become visible) much later. Dedup makes that free.
// The run log — what was called, how long it took, what came back, and the
// digest the run produced — is kept in a small JSON file for the UI.
//
// Day 19 appends the composition pipeline to the same run, right after
// ingestion and before the digests:
//
//	worklog.list_events(all=true)   the entire stored history, not just this run's window
//	worklog.build_sessions          pure: groups it into WorkSession blocks
//	worklog.save_sessions           replaces the stored sessions with the result
//
// Rebuilding from the whole history (not just the freshly ingested window)
// avoids boundary bugs — a session can straddle two collection runs — at the
// cost of a full recompute every time, which is cheap at this data size.
// "Auto after each collect" from the day's plan means exactly this: no
// separate scheduler, no separate button — every run of this one already
// ends with sessions rebuilt.

const (
	defaultCollectInterval = 15 * time.Minute
	minCollectInterval     = time.Minute
	// collectBackfill is how far back the first run (empty worklog) reaches.
	collectBackfill   = 30 * 24 * time.Hour
	collectOverlap    = 48 * time.Hour
	collectChunk      = 7 * 24 * time.Hour
	collectStartDelay = 5 * time.Second
	collectRunTimeout = 5 * time.Minute
	maxStoredRuns     = 20
	githubSource      = "github"
)

var (
	ErrCollectorDisabled = errors.New("collector disabled")
	ErrCollectorBusy     = errors.New("collector already running")
)

// CollectorStep is one MCP tool call a run made.
type CollectorStep struct {
	Server     string `json:"server"`
	Tool       string `json:"tool"`
	Detail     string `json:"detail,omitempty"` // the call's arguments, human-readable
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	Summary    string `json:"summary,omitempty"` // what came back, human-readable
}

// DigestTotals is the headline of one worklog digest — what a run reports
// as its periodic summary.
type DigestTotals struct {
	From       time.Time      `json:"from"`
	To         time.Time      `json:"to"`
	Total      int            `json:"total"`
	Counts     map[string]int `json:"counts"`
	ActiveDays int            `json:"active_days"`
	TopRepos   []RepoTotal    `json:"top_repos"`
}

type RepoTotal struct {
	Repo  string `json:"repo"`
	Total int    `json:"total"`
}

// RunDigest is the summary a run produced after storing events.
type RunDigest struct {
	Today DigestTotals `json:"today"`
	Week  DigestTotals `json:"week"`
}

// SessionsRebuild is what the day-19 pipeline step of a run produced.
type SessionsRebuild struct {
	EventsIn    int `json:"events_in"`
	SessionsOut int `json:"sessions_out"`
	Replaced    int `json:"replaced" jsonschema:"how many sessions existed before this rebuild"`
}

// CollectorRun is one execution of the collection chain.
type CollectorRun struct {
	Trigger     string           `json:"trigger"` // startup | schedule | manual
	StartedAt   time.Time        `json:"started_at"`
	FinishedAt  *time.Time       `json:"finished_at,omitempty"`
	Status      string           `json:"status"` // running | ok | error
	Error       string           `json:"error,omitempty"`
	Backfill    bool             `json:"backfill"`
	WindowSince *time.Time       `json:"window_since,omitempty"`
	WindowUntil *time.Time       `json:"window_until,omitempty"`
	Fetched     int              `json:"fetched"`
	Inserted    int              `json:"inserted"`
	Duplicates  int              `json:"duplicates"`
	Warnings    []string         `json:"warnings"`
	Steps       []CollectorStep  `json:"steps"`
	Digest      *RunDigest       `json:"digest,omitempty"`
	Sessions    *SessionsRebuild `json:"sessions,omitempty"`
}

func (r *CollectorRun) clone() CollectorRun {
	c := *r
	c.Warnings = append([]string{}, r.Warnings...)
	c.Steps = append([]CollectorStep{}, r.Steps...)
	return c
}

// Collector owns the schedule and the run log.
type Collector struct {
	github, worklog *mcpclient.Conn
	interval        time.Duration // 0: manual runs only
	disabledReason  string
	runsPath        string

	mu      sync.Mutex
	current *CollectorRun
	runs    []CollectorRun // newest first
	nextRun *time.Time
}

func NewCollector(github, worklog *mcpclient.Conn, interval time.Duration, runsPath string) *Collector {
	c := &Collector{github: github, worklog: worklog, interval: interval, runsPath: runsPath, runs: []CollectorRun{}}
	if cfg := github.Config(); cfg.Token() == "" {
		c.disabledReason = cfg.TokenEnv + " не задан в backend/.env — собирать активность неоткуда"
	}
	if data, err := os.ReadFile(runsPath); err == nil {
		if err := json.Unmarshal(data, &c.runs); err != nil {
			log.Printf("collector: ignoring unreadable %s: %v", runsPath, err)
			c.runs = []CollectorRun{}
		}
	}
	// A run that was in flight when the process stopped never finished.
	for i := range c.runs {
		if c.runs[i].Status == "running" {
			c.runs[i].Status = "error"
			c.runs[i].Error = "прервано перезапуском сервера"
		}
	}
	return c
}

// collectInterval reads COLLECT_INTERVAL: a Go duration ("15m", "1h"), or
// "0"/"off" for manual runs only.
func collectInterval() time.Duration {
	v := strings.TrimSpace(os.Getenv("COLLECT_INTERVAL"))
	switch v {
	case "":
		return defaultCollectInterval
	case "0", "off":
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < minCollectInterval {
		log.Printf("invalid COLLECT_INTERVAL %q (want a duration of at least %s, or off), using %s", v, minCollectInterval, defaultCollectInterval)
		return defaultCollectInterval
	}
	return d
}

// Start runs the schedule until ctx ends: one run shortly after startup (so
// a fresh instance backfills right away), then every interval. A scheduled
// run that finds a manual one in progress is skipped, not queued.
func (c *Collector) Start(ctx context.Context) {
	if c.disabledReason != "" {
		log.Printf("collector: disabled: %s", c.disabledReason)
		return
	}
	if c.interval == 0 {
		log.Printf("collector: schedule off (COLLECT_INTERVAL=off), manual runs only")
		return
	}
	log.Printf("collector: every %s, first run in %s", c.interval, collectStartDelay)
	go func() {
		next := time.Now().Add(collectStartDelay)
		trigger := "startup"
		for {
			c.mu.Lock()
			c.nextRun = &next
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
			}
			if err := c.run(trigger); errors.Is(err, ErrCollectorBusy) {
				log.Printf("collector: scheduled run skipped, a run is already in progress")
			}
			trigger = "schedule"
			next = next.Add(c.interval)
			if now := time.Now(); next.Before(now) {
				next = now.Add(c.interval)
			}
		}
	}()
}

// Trigger starts a manual run in the background.
func (c *Collector) Trigger() error {
	if c.disabledReason != "" {
		return ErrCollectorDisabled
	}
	run, err := c.begin("manual")
	if err != nil {
		return err
	}
	go c.execute(run)
	return nil
}

// run executes one collection, synchronously.
func (c *Collector) run(trigger string) error {
	run, err := c.begin(trigger)
	if err != nil {
		return err
	}
	c.execute(run)
	return nil
}

// begin registers a new run as current, unless one is already in progress.
func (c *Collector) begin(trigger string) (*CollectorRun, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != nil {
		return nil, ErrCollectorBusy
	}
	c.current = &CollectorRun{Trigger: trigger, StartedAt: time.Now(), Status: "running", Warnings: []string{}, Steps: []CollectorStep{}}
	return c.current, nil
}

func (c *Collector) execute(run *CollectorRun) {
	ctx, cancel := context.WithTimeout(context.Background(), collectRunTimeout)
	defer cancel()
	err := c.collect(ctx, run)

	c.mu.Lock()
	finished := time.Now()
	run.FinishedAt = &finished
	if err != nil {
		run.Status = "error"
		run.Error = err.Error()
	} else {
		run.Status = "ok"
	}
	c.current = nil
	c.runs = append([]CollectorRun{run.clone()}, c.runs...)
	if len(c.runs) > maxStoredRuns {
		c.runs = c.runs[:maxStoredRuns]
	}
	snapshot := append([]CollectorRun{}, c.runs...)
	c.mu.Unlock()

	if err != nil {
		log.Printf("collector: %s run failed after %s: %v", run.Trigger, finished.Sub(run.StartedAt).Round(time.Millisecond), err)
	} else {
		log.Printf("collector: %s run ok in %s: %d fetched, %d new, %d duplicate(s); %d session(s) rebuilt; today %d event(s), 7 days %d",
			run.Trigger, finished.Sub(run.StartedAt).Round(time.Millisecond), run.Fetched, run.Inserted, run.Duplicates,
			run.Sessions.SessionsOut, run.Digest.Today.Total, run.Digest.Week.Total)
	}
	c.saveRuns(snapshot)
}

func (c *Collector) saveRuns(runs []CollectorRun) {
	data, err := json.MarshalIndent(runs, "", "  ")
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(c.runsPath), 0o755); err == nil {
			err = os.WriteFile(c.runsPath, data, 0o644)
		}
	}
	if err != nil {
		log.Printf("collector: cannot save run log: %v", err)
	}
}

// collect is the chain itself. Each chunk is fetched and ingested before
// the next one, so a failure mid-backfill keeps everything up to it.
func (c *Collector) collect(ctx context.Context, run *CollectorRun) error {
	var state struct {
		Sources []struct {
			Source      string    `json:"source"`
			SyncedUntil time.Time `json:"synced_until"`
		} `json:"sources"`
		TotalEvents int `json:"total_events"`
	}
	if err := c.call(ctx, run, c.worklog, "get_sync_state", nil, "", &state, func() string {
		for _, s := range state.Sources {
			if s.Source == githubSource {
				return fmt.Sprintf("курсор %s: %s · в журнале %d событий", s.Source, s.SyncedUntil.Format("02.01 15:04"), state.TotalEvents)
			}
		}
		return "журнал пуст — первый сбор, история за 30 дней"
	}); err != nil {
		return err
	}

	now := time.Now()
	since := now.Add(-collectBackfill)
	run.Backfill = true
	for _, s := range state.Sources {
		if s.Source == githubSource && s.SyncedUntil.After(since) {
			since = s.SyncedUntil.Add(-collectOverlap)
			run.Backfill = false
		}
	}
	since = since.Truncate(time.Second)
	now = now.Truncate(time.Second)
	c.mu.Lock()
	run.WindowSince, run.WindowUntil = &since, &now
	c.mu.Unlock()

	for from := since; from.Before(now); from = from.Add(collectChunk) {
		to := from.Add(collectChunk)
		if to.After(now) {
			to = now
		}
		var fetched struct {
			Events    []json.RawMessage `json:"events"`
			Truncated bool              `json:"truncated"`
			Warnings  []string          `json:"warnings"`
		}
		window := fmt.Sprintf("%s — %s", from.Format("02.01 15:04"), to.Format("02.01 15:04"))
		args := map[string]any{"since": from.Format(time.RFC3339), "until": to.Format(time.RFC3339)}
		if err := c.call(ctx, run, c.github, "get_activity", args, window, &fetched, func() string {
			return fmt.Sprintf("%d событий", len(fetched.Events))
		}); err != nil {
			return err
		}

		var ingested struct {
			Inserted   int `json:"inserted"`
			Duplicates int `json:"duplicates"`
		}
		args = map[string]any{
			"source": githubSource, "events": fetched.Events,
			"window_since": from.Format(time.RFC3339), "window_until": to.Format(time.RFC3339),
		}
		if fetched.Events == nil {
			args["events"] = []json.RawMessage{}
		}
		if err := c.call(ctx, run, c.worklog, "ingest_events", args, fmt.Sprintf("%d событий", len(fetched.Events)), &ingested, func() string {
			return fmt.Sprintf("+%d новых, %d уже были", ingested.Inserted, ingested.Duplicates)
		}); err != nil {
			return err
		}

		c.mu.Lock()
		run.Fetched += len(fetched.Events)
		run.Inserted += ingested.Inserted
		run.Duplicates += ingested.Duplicates
		run.Warnings = append(run.Warnings, fetched.Warnings...)
		if fetched.Truncated {
			run.Warnings = append(run.Warnings, "за "+window+" событий больше 500 — часть не сохранена")
		}
		c.mu.Unlock()
	}

	if err := c.rebuildSessions(ctx, run); err != nil {
		return err
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	digest := &RunDigest{}
	for _, p := range []struct {
		label string
		from  time.Time
		out   *DigestTotals
	}{
		{"сегодня", today, &digest.Today},
		{"7 дней", today.AddDate(0, 0, -6), &digest.Week},
	} {
		var d struct {
			DigestTotals
			ByRepo []RepoTotal `json:"by_repo"`
		}
		args := map[string]any{"from": p.from.Format("2006-01-02")}
		if err := c.call(ctx, run, c.worklog, "get_activity_digest", args, p.label, &d, func() string {
			return digestLine(d.Total, d.Counts)
		}); err != nil {
			return err
		}
		*p.out = d.DigestTotals
		p.out.TopRepos = d.ByRepo[:min(3, len(d.ByRepo))]
	}
	c.mu.Lock()
	run.Digest = digest
	c.mu.Unlock()
	return nil
}

// rebuildSessions runs day 19's composition pipeline: the entire stored
// history in, work sessions out, replacing whatever was stored before.
func (c *Collector) rebuildSessions(ctx context.Context, run *CollectorRun) error {
	var all struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := c.call(ctx, run, c.worklog, "list_events", map[string]any{"from": "1970-01-01", "all": true}, "вся история", &all, func() string {
		return fmt.Sprintf("%d событий", len(all.Events))
	}); err != nil {
		return err
	}

	var built struct {
		Sessions    []json.RawMessage `json:"sessions"`
		SessionsOut int               `json:"sessions_out"`
	}
	if err := c.call(ctx, run, c.worklog, "build_sessions", map[string]any{"events": all.Events}, fmt.Sprintf("%d событий", len(all.Events)), &built, func() string {
		return fmt.Sprintf("%d сессий", built.SessionsOut)
	}); err != nil {
		return err
	}

	var saved struct {
		Saved    int `json:"saved"`
		Replaced int `json:"replaced"`
	}
	if err := c.call(ctx, run, c.worklog, "save_sessions", map[string]any{"sessions": built.Sessions}, fmt.Sprintf("%d сессий", len(built.Sessions)), &saved, func() string {
		return fmt.Sprintf("сохранено %d (было %d)", saved.Saved, saved.Replaced)
	}); err != nil {
		return err
	}

	c.mu.Lock()
	run.Sessions = &SessionsRebuild{EventsIn: len(all.Events), SessionsOut: saved.Saved, Replaced: saved.Replaced}
	c.mu.Unlock()
	return nil
}

// call runs one tool, records it as a step, and decodes its structured
// result into out. summarize runs after a successful decode.
func (c *Collector) call(ctx context.Context, run *CollectorRun, conn *mcpclient.Conn, tool string, args map[string]any, detail string, out any, summarize func() string) error {
	cfg := conn.Config()
	step := CollectorStep{Server: cfg.ID, Tool: tool, Detail: detail}
	if args == nil {
		args = map[string]any{}
	}
	started := time.Now()
	res, err := conn.Call(ctx, tool, args)
	step.DurationMs = time.Since(started).Milliseconds()
	switch {
	case err != nil:
		step.Error = err.Error()
	case res.IsError:
		step.Error = res.Text
	default:
		raw := res.Structured
		if len(raw) == 0 {
			raw = json.RawMessage(res.Text)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			step.Error = "непонятный ответ: " + err.Error()
		} else {
			step.OK = true
			step.Summary = summarize()
		}
	}
	c.mu.Lock()
	run.Steps = append(run.Steps, step)
	c.mu.Unlock()
	if !step.OK {
		return fmt.Errorf("%s.%s: %s", cfg.ID, tool, step.Error)
	}
	return nil
}

var kindLabels = []struct{ kind, one, few, many string }{
	{"commit", "коммит", "коммита", "коммитов"},
	{"pr_opened", "PR создан", "PR создано", "PR создано"},
	{"pr_merged", "PR смёржен", "PR смёржено", "PR смёржено"},
	{"review", "ревью", "ревью", "ревью"},
	{"issue_comment", "комментарий", "комментария", "комментариев"},
}

// digestLine is a one-line Russian summary, e.g. "12 событий: 9 коммитов,
// 2 PR смёржено, 1 ревью".
func digestLine(total int, counts map[string]int) string {
	if total == 0 {
		return "событий нет"
	}
	var parts []string
	for _, k := range kindLabels {
		if n := counts[k.kind]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, pluralRu(n, k.one, k.few, k.many)))
		}
	}
	return fmt.Sprintf("%d %s: %s", total, pluralRu(total, "событие", "события", "событий"), strings.Join(parts, ", "))
}

func pluralRu(n int, one, few, many string) string {
	mod10, mod100 := n%10, n%100
	switch {
	case mod10 == 1 && mod100 != 11:
		return one
	case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
		return few
	}
	return many
}

// CollectorStatus is the «Активность» screen's view of the collector.
type CollectorStatus struct {
	Enabled         bool            `json:"enabled"`
	DisabledReason  string          `json:"disabled_reason,omitempty"`
	IntervalSeconds int64           `json:"interval_seconds"` // 0: manual only
	NextRunAt       *time.Time      `json:"next_run_at,omitempty"`
	Current         *CollectorRun   `json:"current,omitempty"`
	Runs            []CollectorRun  `json:"runs"`
	Sync            json.RawMessage `json:"sync,omitempty"` // worklog get_sync_state
	SyncError       string          `json:"sync_error,omitempty"`
}

func (c *Collector) Status(ctx context.Context) CollectorStatus {
	c.mu.Lock()
	st := CollectorStatus{
		Enabled:         c.disabledReason == "",
		DisabledReason:  c.disabledReason,
		IntervalSeconds: int64(c.interval / time.Second),
		Runs:            append([]CollectorRun{}, c.runs...),
	}
	if c.current != nil {
		cur := c.current.clone()
		st.Current = &cur
	} else if c.nextRun != nil && st.Enabled {
		next := *c.nextRun
		st.NextRunAt = &next
	}
	c.mu.Unlock()

	res, err := c.worklog.Call(ctx, "get_sync_state", map[string]any{})
	switch {
	case err != nil:
		st.SyncError = err.Error()
	case res.IsError:
		st.SyncError = res.Text
	default:
		st.Sync = res.Structured
	}
	return st
}
