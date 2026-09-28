package main

import (
	"testing"
	"time"

	"aiwork/backend/internal/activity"
)

func ev(id string, kind activity.Kind, repo string, at time.Time) activity.Event {
	return activity.Event{ID: id, Source: "github", Kind: kind, Repo: repo, Title: id, URL: "https://x/" + id, OccurredAt: at, Author: "me"}
}

func mev(id, title string, start, end time.Time) activity.Event {
	return activity.Event{ID: id, Source: "calendar", Kind: activity.KindMeeting, Title: title, OccurredAt: start, EndsAt: end}
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

// TestBuildSessions_ParallelReposNeverMerge is the fix for a real bug: events
// in two repositories within the same time window used to collapse into one
// session, with the repo that had fewer events silently dropped from every
// chart. Now each repository gets its own session, both counted in full.
func TestBuildSessions_ParallelReposNeverMerge(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	events := []activity.Event{
		ev("a", activity.KindCommit, "org/repo-a", base),
		ev("b", activity.KindCommit, "org/repo-b", base.Add(time.Minute)),
		ev("c", activity.KindCommit, "org/repo-b", base.Add(2*time.Minute)),
	}
	sessions := buildSessions(events)
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions (one per repo), got %d: %+v", len(sessions), sessions)
	}
	byRepo := map[string]WorkSession{}
	for _, s := range sessions {
		byRepo[s.Repo] = s
	}
	a, ok := byRepo["org/repo-a"]
	if !ok || a.EventCount != 1 {
		t.Errorf("want repo-a's session with 1 event, got %+v (present: %v)", a, ok)
	}
	b, ok := byRepo["org/repo-b"]
	if !ok || b.EventCount != 2 {
		t.Errorf("want repo-b's session with 2 events, got %+v (present: %v)", b, ok)
	}
}

// TestBuildSessions_MeetingsBecomeOwnSessions checks day 20's meeting
// branch: each calendar event becomes its own kind=meeting session, using
// its own start/end directly (no gap-merging, no lead-in), independent of
// whatever work events fall in the same period.
func TestBuildSessions_MeetingsBecomeOwnSessions(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	events := []activity.Event{
		ev("a", activity.KindCommit, "r/1", base),
		mev("m1", "Standup", base.Add(time.Hour), base.Add(90*time.Minute)),
	}
	sessions := buildSessions(events)
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions (1 work, 1 meeting), got %d: %+v", len(sessions), sessions)
	}
	var meeting WorkSession
	for _, s := range sessions {
		if s.Kind == SessionKindMeeting {
			meeting = s
		}
	}
	if meeting.Title != "Standup" || meeting.Repo != "" {
		t.Fatalf("meeting session: want title Standup, no repo, got %+v", meeting)
	}
	wantStart, wantEnd := base.Add(time.Hour), base.Add(90*time.Minute)
	if !meeting.Start.Equal(wantStart) || !meeting.End.Equal(wantEnd) {
		t.Errorf("meeting session bounds: want %v..%v (no lead-in), got %v..%v", wantStart, wantEnd, meeting.Start, meeting.End)
	}
}

// TestBuildSessions_MeetingWithoutEndIsDropped guards against fabricating a
// duration for malformed calendar data.
func TestBuildSessions_MeetingWithoutEndIsDropped(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	sessions := buildSessions([]activity.Event{mev("m1", "Bad", base, time.Time{})})
	if len(sessions) != 0 {
		t.Fatalf("want a meeting with no end dropped, got %d session(s): %+v", len(sessions), sessions)
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
