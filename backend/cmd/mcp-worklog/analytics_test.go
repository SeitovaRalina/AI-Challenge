package main

import "testing"

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
