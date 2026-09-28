package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aiwork/backend/internal/activity"
)

func openTestStore(t *testing.T) *store {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "worklog.db"))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func calEvent(id, title string, at time.Time) activity.Event {
	return activity.Event{ID: id, Source: "calendar", Kind: activity.KindMeeting, Title: title, OccurredAt: at, EndsAt: at.Add(time.Hour)}
}

// TestIngest_ReplaceReconcilesTheWindow is the fix for a real bug: unlike
// GitHub commits (an immutable log), a calendar meeting can be moved,
// renamed or deleted after the fact — a plain dedup-by-id insert left a
// stale row behind forever once the source stopped reporting it. replace
// deletes every existing row of that source inside [since, until] before
// inserting the fresh batch, so a moved-away meeting actually disappears.
func TestIngest_ReplaceReconcilesTheWindow(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	base := time.Date(2026, 9, 25, 16, 0, 0, 0, time.UTC)
	windowSince, windowUntil := base.Add(-24*time.Hour), base.Add(24*time.Hour)

	// First run: two meetings land in the window.
	if _, err := s.ingest(ctx, "calendar", []activity.Event{
		calEvent("calendar:a", "Stays put", base),
		calEvent("calendar:b", "Gets moved away", base.Add(time.Hour)),
	}, windowSince, windowUntil, true); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	// Second run over the same window: "b" was moved out of it (the source
	// no longer reports it), only "a" (unchanged) and a new "c" come back.
	res, err := s.ingest(ctx, "calendar", []activity.Event{
		calEvent("calendar:a", "Stays put", base),
		calEvent("calendar:c", "Newly added", base.Add(2*time.Hour)),
	}, windowSince, windowUntil, true)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if res.deleted == 0 {
		t.Errorf("want the window's previous rows deleted before reinsert, got deleted=%d", res.deleted)
	}

	events, err := s.events(ctx, eventFilter{from: windowSince, to: windowUntil}, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range events {
		ids[e.ID] = true
	}
	if ids["calendar:b"] {
		t.Errorf("want calendar:b gone (moved out of the window on the second fetch), still present: %v", ids)
	}
	if !ids["calendar:a"] || !ids["calendar:c"] {
		t.Errorf("want calendar:a and calendar:c present, got %v", ids)
	}
	if len(events) != 2 {
		t.Errorf("want exactly 2 events after reconciliation, got %d: %v", len(events), events)
	}
}

// TestIngest_ReplaceLeavesOtherSourcesAndWindowsAlone guards the DELETE's
// scope: it must never touch another source's events, or this source's own
// events outside the window being reconciled.
func TestIngest_ReplaceLeavesOtherSourcesAndWindowsAlone(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	base := time.Date(2026, 9, 25, 16, 0, 0, 0, time.UTC)
	windowSince, windowUntil := base.Add(-time.Hour), base.Add(time.Hour)

	outsideWindow := calEvent("calendar:old", "Last month", base.AddDate(0, 0, -30))
	otherSource := activity.Event{ID: "github:commit:x", Source: "github", Kind: activity.KindCommit, Repo: "r/1", Title: "x", OccurredAt: base}
	if _, err := s.ingest(ctx, "calendar", []activity.Event{outsideWindow}, base.AddDate(0, 0, -31), base.AddDate(0, 0, -29), true); err != nil {
		t.Fatalf("seed calendar: %v", err)
	}
	if _, err := s.ingest(ctx, "github", []activity.Event{otherSource}, windowSince, windowUntil, false); err != nil {
		t.Fatalf("seed github: %v", err)
	}

	if _, err := s.ingest(ctx, "calendar", []activity.Event{calEvent("calendar:new", "This hour", base)}, windowSince, windowUntil, true); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	all, err := s.events(ctx, eventFilter{from: base.AddDate(0, 0, -40), to: base.AddDate(0, 0, 1)}, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range all {
		ids[e.ID] = true
	}
	for _, want := range []string{"calendar:old", "github:commit:x", "calendar:new"} {
		if !ids[want] {
			t.Errorf("want %s untouched by the reconcile of an unrelated window/source, got %v", want, ids)
		}
	}
}
