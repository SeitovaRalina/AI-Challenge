package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"aiwork/backend/internal/activity"
)

// store is the worklog's SQLite database: raw activity events, keyed by
// (source, id) so re-ingesting an overlapping window never duplicates
// anything, and one sync-state row per source — the cursor that makes the
// background collector restart-safe.
type store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS events (
	source      TEXT    NOT NULL,
	id          TEXT    NOT NULL,
	kind        TEXT    NOT NULL,
	repo        TEXT    NOT NULL,
	title       TEXT    NOT NULL,
	url         TEXT    NOT NULL,
	occurred_at INTEGER NOT NULL, -- unix seconds
	author      TEXT    NOT NULL DEFAULT '',
	ref         TEXT    NOT NULL DEFAULT '',
	ingested_at INTEGER NOT NULL,
	PRIMARY KEY (source, id)
);
CREATE INDEX IF NOT EXISTS events_occurred_at ON events (occurred_at);

CREATE TABLE IF NOT EXISTS sync_state (
	source       TEXT    PRIMARY KEY,
	synced_since INTEGER NOT NULL, -- start of the continuously covered range
	synced_until INTEGER NOT NULL, -- end of it: the collector's cursor
	updated_at   INTEGER NOT NULL
);
`

func openStore(path string) (*store, error) {
	// WAL + busy_timeout: readers never block the writer, and a second
	// process opening the same file waits instead of failing with "database
	// is locked".
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &store{db: db}, nil
}

func (s *store) Close() error { return s.db.Close() }

// SyncState is one source's covered range.
type SyncState struct {
	Source      string    `json:"source"`
	SyncedSince time.Time `json:"synced_since" jsonschema:"events from here on are stored continuously"`
	SyncedUntil time.Time `json:"synced_until" jsonschema:"events up to here are stored; later ones are not collected yet"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ingestResult struct {
	inserted, duplicates int
	state                SyncState
}

// ingest stores events and advances the source's cursor in one transaction,
// so a crash can never leave the cursor ahead of the data it claims.
func (s *store) ingest(ctx context.Context, source string, events []activity.Event, since, until time.Time) (ingestResult, error) {
	var res ingestResult
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events (source, id, kind, repo, title, url, occurred_at, author, ref, ingested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (source, id) DO NOTHING`)
	if err != nil {
		return res, err
	}
	defer stmt.Close()
	now := time.Now().Unix()
	for _, e := range events {
		r, err := stmt.ExecContext(ctx, source, e.ID, string(e.Kind), e.Repo, e.Title, e.URL, e.OccurredAt.Unix(), e.Author, e.Ref, now)
		if err != nil {
			return res, fmt.Errorf("event %s: %w", e.ID, err)
		}
		if n, _ := r.RowsAffected(); n > 0 {
			res.inserted++
		} else {
			res.duplicates++
		}
	}

	// The covered range grows only while windows touch it; a window that
	// starts after the current cursor leaves a gap, so coverage restarts
	// from that window — callers must not assume the gap was collected.
	state := SyncState{Source: source, SyncedSince: since, SyncedUntil: until}
	var prevSince, prevUntil int64
	err = tx.QueryRowContext(ctx, `SELECT synced_since, synced_until FROM sync_state WHERE source = ?`, source).Scan(&prevSince, &prevUntil)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return res, err
	case since.Unix() <= prevUntil:
		state.SyncedSince = minTime(since, time.Unix(prevSince, 0))
		state.SyncedUntil = maxTime(until, time.Unix(prevUntil, 0))
	}
	state.UpdatedAt = time.Unix(now, 0)
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_state (source, synced_since, synced_until, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (source) DO UPDATE SET synced_since = excluded.synced_since, synced_until = excluded.synced_until, updated_at = excluded.updated_at`,
		source, state.SyncedSince.Unix(), state.SyncedUntil.Unix(), now); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	res.state = localState(state)
	return res, nil
}

func (s *store) syncStates(ctx context.Context) ([]SyncState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source, synced_since, synced_until, updated_at FROM sync_state ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := []SyncState{}
	for rows.Next() {
		var st SyncState
		var since, until, updated int64
		if err := rows.Scan(&st.Source, &since, &until, &updated); err != nil {
			return nil, err
		}
		st.SyncedSince, st.SyncedUntil, st.UpdatedAt = time.Unix(since, 0), time.Unix(until, 0), time.Unix(updated, 0)
		states = append(states, localState(st))
	}
	return states, rows.Err()
}

func (s *store) countEvents(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&n)
	return n, err
}

// eventFilter selects events in [from, to] (inclusive), optionally narrowed
// to repositories and kinds.
type eventFilter struct {
	from, to time.Time
	repos    []string
	kinds    []activity.Kind
}

func (f eventFilter) where() (string, []any) {
	clauses := []string{"occurred_at >= ?", "occurred_at <= ?"}
	args := []any{f.from.Unix(), f.to.Unix()}
	if len(f.repos) > 0 {
		clauses = append(clauses, "repo IN ("+placeholders(len(f.repos))+")")
		for _, r := range f.repos {
			args = append(args, r)
		}
	}
	if len(f.kinds) > 0 {
		clauses = append(clauses, "kind IN ("+placeholders(len(f.kinds))+")")
		for _, k := range f.kinds {
			args = append(args, string(k))
		}
	}
	return strings.Join(clauses, " AND "), args
}

// events returns matching events, newest first; limit <= 0 means all.
func (s *store) events(ctx context.Context, f eventFilter, limit int) ([]activity.Event, error) {
	where, args := f.where()
	query := `SELECT source, id, kind, repo, title, url, occurred_at, author, ref FROM events WHERE ` + where + ` ORDER BY occurred_at DESC, id`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []activity.Event{}
	for rows.Next() {
		var e activity.Event
		var kind string
		var at int64
		if err := rows.Scan(&e.Source, &e.ID, &kind, &e.Repo, &e.Title, &e.URL, &at, &e.Author, &e.Ref); err != nil {
			return nil, err
		}
		e.Kind = activity.Kind(kind)
		e.OccurredAt = time.Unix(at, 0).In(time.Local)
		events = append(events, e)
	}
	return events, rows.Err()
}

// countByKind counts every matching event per kind (not just a page of them).
func (s *store) countByKind(ctx context.Context, f eventFilter) (map[activity.Kind]int, int, error) {
	where, args := f.where()
	rows, err := s.db.QueryContext(ctx, `SELECT kind, COUNT(*) FROM events WHERE `+where+` GROUP BY kind`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	counts := map[activity.Kind]int{}
	total := 0
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, 0, err
		}
		counts[activity.Kind(kind)] = n
		total += n
	}
	return counts, total, rows.Err()
}

// repoNames lists every repository with stored events, for resolving a bare
// repo name to owner/repo.
func (s *store) repoNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT repo FROM events ORDER BY repo`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var repos []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func localState(st SyncState) SyncState {
	st.SyncedSince = st.SyncedSince.In(time.Local)
	st.SyncedUntil = st.SyncedUntil.In(time.Local)
	st.UpdatedAt = st.UpdatedAt.In(time.Local)
	return st
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
