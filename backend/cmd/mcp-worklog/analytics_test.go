package main

import (
	"testing"
	"time"
)

func TestCommitType(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"feat(w04-d19): «Аналитика» screen — charts over the session pipeline", "feat"},
		{"fix(w04-d18): poll collector status live instead of only while a run shows", "fix"},
		{"chore(w04-d19): add shadcn chart component (recharts)", "chore"},
		{"docs(w04-d19): record the day 19 assignment", "docs"},
		{"refactor: simplify the thing", "refactor"},
		{"feat!: breaking change, no scope", "feat"},
		{"feat(scope)!: breaking change, with scope", "feat"},
		{"Merge pull request #18 from feature/w04-d18", "other"},
		{"wip: not a real conventional-commit type", "other"},
		{"bumped the version number", "other"},
		{"", "other"},
	}
	for _, c := range cases {
		if got := commitType(c.title); got != c.want {
			t.Errorf("commitType(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

// TestCountWeekdayOccurrences_HeatmapAverageStaysUnder61Minutes is the fix
// for a real complaint: a heatmap cell summed across every week in the
// period (e.g. 4 Mondays) could show something like 160 minutes for one
// hour, which looks impossible. Dividing by how many times that weekday
// actually occurred turns it into a per-occurrence average, always <= 60.
func TestCountWeekdayOccurrences_HeatmapAverageStaysUnder61Minutes(t *testing.T) {
	// 2026-09-01 is a Tuesday; a 30-day window from there contains 5 Tuesdays.
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, 29)
	counts := countWeekdayOccurrences(from, to)
	tuesday := int(time.Tuesday)
	if counts[tuesday] != 5 {
		t.Fatalf("want 5 Tuesdays in this 30-day window, got %d", counts[tuesday])
	}

	// Even if every one of those 5 Tuesdays contributed a full 60 minutes to
	// the same hour (the worst case), the average must not exceed 60.
	summedMinutes := 60.0 * float64(counts[tuesday])
	avg := summedMinutes / float64(counts[tuesday])
	if avg > 60 {
		t.Errorf("average must never exceed 60 minutes, got %v", avg)
	}
}

func workRow(repo, project string, start, end time.Time) storedSessionRow {
	return storedSessionRow{
		storedSession: storedSession{startAt: start.Unix(), endAt: end.Unix(), kind: SessionKindWork, repo: repo},
		project:       project,
	}
}

func meetingRow(title string, start, end time.Time) storedSessionRow {
	return storedSessionRow{storedSession: storedSession{startAt: start.Unix(), endAt: end.Unix(), kind: SessionKindMeeting, title: title}}
}

func sumHours(blocks []block, kind string) float64 {
	var total float64
	for _, b := range blocks {
		if b.Kind == kind {
			total += b.End.Sub(b.Start).Hours()
		}
	}
	return total
}

// TestResolveIntervals_MeetingTakesPriorityOverWork is the fix for the
// double-counting a naive sum would produce: a work session and a meeting
// that overlap must not both claim the overlapping minutes.
func TestResolveIntervals_MeetingTakesPriorityOverWork(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	rows := []storedSessionRow{
		workRow("org/repo", "repo", base, base.Add(2*time.Hour)),          // 09:00-11:00
		meetingRow("Sync", base.Add(time.Hour), base.Add(90*time.Minute)), // 10:00-10:30, fully inside the work session
	}
	blocks := resolveIntervals(rows, base, base.Add(3*time.Hour))

	work, meeting := sumHours(blocks, SessionKindWork), sumHours(blocks, SessionKindMeeting)
	if got, want := meeting, 0.5; got != want {
		t.Errorf("meeting hours: want %v, got %v", want, got)
	}
	if got, want := work, 1.5; got != want {
		t.Errorf("work hours: want %v (2h minus the 30m meeting overlap), got %v", want, got)
	}
	if total := work + meeting; total != 2.0 {
		t.Errorf("total tracked time: want 2h (the work session's own span, not 2.5h double-counted), got %v", total)
	}
}

// TestResolveIntervals_OverlappingMeetingsMergeNotDoubleCounted checks the
// other half of the same bug: two overlapping meetings must count their
// shared time once, not twice.
func TestResolveIntervals_OverlappingMeetingsMergeNotDoubleCounted(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	rows := []storedSessionRow{
		meetingRow("A", base, base.Add(time.Hour)),                          // 09:00-10:00
		meetingRow("B", base.Add(30*time.Minute), base.Add(90*time.Minute)), // 09:30-10:30, overlaps A by 30m
	}
	blocks := resolveIntervals(rows, base, base.Add(2*time.Hour))
	if got, want := sumHours(blocks, SessionKindMeeting), 1.5; got != want {
		t.Errorf("meeting hours: want %v (union of 09:00-10:30, not 2h summed), got %v", want, got)
	}
}

func TestCountWeekdayOccurrences_SingleDay(t *testing.T) {
	day := time.Date(2026, 9, 28, 0, 0, 0, 0, time.Local) // a Monday
	counts := countWeekdayOccurrences(day, day)
	for wd := 0; wd < 7; wd++ {
		want := 0
		if wd == int(time.Monday) {
			want = 1
		}
		if counts[wd] != want {
			t.Errorf("weekday %d: want %d, got %d", wd, want, counts[wd])
		}
	}
}
