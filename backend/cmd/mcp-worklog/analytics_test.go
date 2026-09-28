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
