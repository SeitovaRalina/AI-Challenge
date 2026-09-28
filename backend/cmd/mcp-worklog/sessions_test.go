package main

import (
	"testing"
	"time"

	"aiwork/backend/internal/activity"
)

func ev(id string, kind activity.Kind, repo string, at time.Time) activity.Event {
	return activity.Event{ID: id, Source: "github", Kind: kind, Repo: repo, Title: id, URL: "https://x/" + id, OccurredAt: at, Author: "me"}
}

func TestBuildSessions_Empty(t *testing.T) {
	sessions := buildSessions(nil)
	if len(sessions) != 0 {
		t.Fatalf("want 0 sessions, got %d", len(sessions))
	}
}

func TestBuildSessions_GapSplitsSessions(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	events := []activity.Event{
		ev("a", activity.KindCommit, "r/1", base),
		ev("b", activity.KindCommit, "r/1", base.Add(30*time.Minute)), // within 45m gap: same session
		ev("c", activity.KindCommit, "r/1", base.Add(2*time.Hour)),    // >45m after b: new session
	}
	sessions := buildSessions(events)
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d: %+v", len(sessions), sessions)
	}
	first, second := sessions[0], sessions[1]
	if first.EventCount != 2 {
		t.Errorf("first session: want 2 events, got %d", first.EventCount)
	}
	wantStart := base.Add(-sessionLeadIn)
	if !first.Start.Equal(wantStart) {
		t.Errorf("first session start: want %v (lead-in before first event), got %v", wantStart, first.Start)
	}
	wantEnd := base.Add(30 * time.Minute)
	if !first.End.Equal(wantEnd) {
		t.Errorf("first session end: want %v (last event's time), got %v", wantEnd, first.End)
	}
	if first.FirstEventID != "a" || first.LastEventID != "b" {
		t.Errorf("first session: want first/last event a/b, got %s/%s", first.FirstEventID, first.LastEventID)
	}
	if second.EventCount != 1 || second.FirstEventID != "c" {
		t.Errorf("second session: want 1 event (c), got %+v", second)
	}
}

func TestBuildSessions_ExactlyAtGapBoundaryStaysOneSession(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	events := []activity.Event{
		ev("a", activity.KindCommit, "r/1", base),
		ev("b", activity.KindCommit, "r/1", base.Add(sessionGap)), // exactly the gap: still one session
	}
	sessions := buildSessions(events)
	if len(sessions) != 1 {
		t.Fatalf("want 1 session at exactly the gap boundary, got %d", len(sessions))
	}
}

func TestBuildSessions_UnsortedInputIsSortedFirst(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	events := []activity.Event{
		ev("b", activity.KindCommit, "r/1", base.Add(10*time.Minute)),
		ev("a", activity.KindCommit, "r/1", base), // out of order in the input slice
	}
	sessions := buildSessions(events)
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0].FirstEventID != "a" || sessions[0].LastEventID != "b" {
		t.Errorf("want chronological first/last a/b regardless of input order, got %s/%s", sessions[0].FirstEventID, sessions[0].LastEventID)
	}
}

func TestBuildSessions_CategoryMajorityWithTieFavorsFirstSeen(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	// One commit (development), then one review (review): a 1-1 tie —
	// whichever kind's event came first in time should win.
	events := []activity.Event{
		ev("a", activity.KindReview, "r/1", base),
		ev("b", activity.KindCommit, "r/1", base.Add(time.Minute)),
	}
	sessions := buildSessions(events)
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0].Category != CategoryReview {
		t.Errorf("want tie to favor the first-seen category (review), got %s", sessions[0].Category)
	}

	// Two commits then one review: development strictly dominates.
	events2 := []activity.Event{
		ev("a", activity.KindCommit, "r/1", base),
		ev("b", activity.KindCommit, "r/1", base.Add(time.Minute)),
		ev("c", activity.KindIssueComment, "r/1", base.Add(2*time.Minute)),
	}
	sessions2 := buildSessions(events2)
	if sessions2[0].Category != CategoryDevelopment {
		t.Errorf("want development to dominate 2-1, got %s", sessions2[0].Category)
	}
}

func TestBuildSessions_RepoMajority(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	events := []activity.Event{
		ev("a", activity.KindCommit, "org/repo-a", base),
		ev("b", activity.KindCommit, "org/repo-b", base.Add(time.Minute)),
		ev("c", activity.KindCommit, "org/repo-b", base.Add(2*time.Minute)),
	}
	sessions := buildSessions(events)
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0].Repo != "org/repo-b" {
		t.Errorf("want the 2-1 dominant repo org/repo-b, got %s", sessions[0].Repo)
	}
}

func TestBuildSessions_Deterministic(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	events := []activity.Event{
		ev("a", activity.KindCommit, "r/1", base),
		ev("b", activity.KindReview, "r/2", base.Add(20*time.Minute)),
		ev("c", activity.KindCommit, "r/1", base.Add(3*time.Hour)),
	}
	first := buildSessions(events)
	second := buildSessions(events)
	if len(first) != len(second) {
		t.Fatalf("non-deterministic session count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("session %d differs between calls: %+v vs %+v", i, first[i], second[i])
		}
	}
}
