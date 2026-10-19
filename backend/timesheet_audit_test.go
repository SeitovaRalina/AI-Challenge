package main

import (
	"testing"
	"time"
)

func testSession(kind, repo string, start time.Time, dur time.Duration) sessionRaw {
	return sessionRaw{Start: start, End: start.Add(dur), Kind: kind, Repo: repo, EventCount: 1}
}

func hasFlag(flags []AuditFlag, f AuditFlag) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

func TestFlagSession_CleanWorkSessionGetsNoFlags(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	s := testSession("work", "org/repo-a", start, 2*time.Hour)
	mapped := map[string]bool{"org/repo-a": true}
	flags := flagSession(s, nil, mapped)
	if len(flags) != 0 {
		t.Fatalf("got %v, want no flags for a clean, mapped work session", flags)
	}
}

func TestFlagSession_UnmappedRepoIsFlagged(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	s := testSession("work", "org/recording-agent", start, time.Hour)
	flags := flagSession(s, nil, map[string]bool{})
	if !hasFlag(flags, FlagUnmappedSolidtimeProject) {
		t.Fatalf("got %v, want unmapped_solidtime_project for a repo with no explicit mapping", flags)
	}
}

func TestFlagSession_MeetingIsNeverFlaggedForUnmappedProject(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	s := sessionRaw{Start: start, End: start.Add(time.Hour), Kind: "meeting", Title: "Созвон"}
	flags := flagSession(s, nil, map[string]bool{})
	if hasFlag(flags, FlagUnmappedSolidtimeProject) {
		t.Fatalf("got %v, a meeting has no repo and must never get unmapped_solidtime_project", flags)
	}
}

func TestFlagSession_OverlapWithExistingSolidtimeEntryIsFlagged(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	s := testSession("work", "org/repo-a", start, 2*time.Hour)
	existing := []solidtimeTimeEntryRaw{{Start: start.Add(30 * time.Minute), End: start.Add(90 * time.Minute)}}
	flags := flagSession(s, existing, map[string]bool{"org/repo-a": true})
	if !hasFlag(flags, FlagPossibleDuplicate) {
		t.Fatalf("got %v, want possible_duplicate when the draft overlaps an already-tracked entry", flags)
	}
}

func TestFlagSession_AdjacentSolidtimeEntryIsNotFlagged(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	s := testSession("work", "org/repo-a", start, time.Hour)
	// Ends exactly when the draft starts — touching, not overlapping.
	existing := []solidtimeTimeEntryRaw{{Start: start.Add(-time.Hour), End: start}}
	flags := flagSession(s, existing, map[string]bool{"org/repo-a": true})
	if hasFlag(flags, FlagPossibleDuplicate) {
		t.Fatalf("got %v, a merely-adjacent entry must not count as a duplicate", flags)
	}
}

func TestFlagSession_ZeroDurationIsFlagged(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	s := testSession("work", "org/repo-a", start, 0)
	flags := flagSession(s, nil, map[string]bool{"org/repo-a": true})
	if !hasFlag(flags, FlagSuspiciousDuration) {
		t.Fatalf("got %v, want suspicious_duration for a zero-length session", flags)
	}
}

func TestFlagSession_ImplausiblyLongSessionIsFlagged(t *testing.T) {
	start := time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC)
	s := testSession("work", "org/repo-a", start, 12*time.Hour)
	flags := flagSession(s, nil, map[string]bool{"org/repo-a": true})
	if !hasFlag(flags, FlagSuspiciousDuration) {
		t.Fatalf("got %v, want suspicious_duration for a 12h single session", flags)
	}
}

func TestFlagSession_PlausibleLongSessionIsNotFlagged(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	s := testSession("work", "org/repo-a", start, 8*time.Hour)
	flags := flagSession(s, nil, map[string]bool{"org/repo-a": true})
	if hasFlag(flags, FlagSuspiciousDuration) {
		t.Fatalf("got %v, an 8h session is within maxPlausibleSessionDuration", flags)
	}
}
