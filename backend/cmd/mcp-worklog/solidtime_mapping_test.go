package main

import (
	"context"
	"testing"
	"time"

	"aiwork/backend/internal/activity"
)

func ghEvent(id, repo string, at time.Time) activity.Event {
	return activity.Event{ID: id, Source: "github", Kind: activity.KindCommit, Repo: repo, Title: "commit", OccurredAt: at}
}

// TestRepoSolidtimeProjects_UnmappedByDefault mirrors repoProjects' own
// contract: a repository with events is listed even with no explicit
// mapping, so the UI can offer it for mapping, not just the ones already
// mapped.
func TestRepoSolidtimeProjects_UnmappedByDefault(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := s.ingest(ctx, "github", []activity.Event{ghEvent("1", "org/repo-a", now)}, now.Add(-time.Hour), now, false); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	rows, err := s.repoSolidtimeProjects(ctx)
	if err != nil {
		t.Fatalf("repoSolidtimeProjects: %v", err)
	}
	if len(rows) != 1 || rows[0].repo != "org/repo-a" || rows[0].solidtimeProjectID != "" {
		t.Fatalf("got %+v, want one unmapped row for org/repo-a", rows)
	}
}

// TestSetRepoSolidtimeProject_SetAndClear is the explicit-mapping
// round trip: a repo and its Solidtime project name routinely differ, so
// this is never guessed — set once, read back, clear with an empty id.
func TestSetRepoSolidtimeProject_SetAndClear(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := s.ingest(ctx, "github", []activity.Event{ghEvent("1", "org/recording-agent", now)}, now.Add(-time.Hour), now, false); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	if err := s.setRepoSolidtimeProject(ctx, "org/recording-agent", "proj-123", "Project Internal: Agents"); err != nil {
		t.Fatalf("setRepoSolidtimeProject: %v", err)
	}
	rows, err := s.repoSolidtimeProjects(ctx)
	if err != nil {
		t.Fatalf("repoSolidtimeProjects: %v", err)
	}
	if len(rows) != 1 || rows[0].solidtimeProjectID != "proj-123" || rows[0].solidtimeProjectName != "Project Internal: Agents" {
		t.Fatalf("got %+v, want the mapping to stick", rows)
	}

	if err := s.setRepoSolidtimeProject(ctx, "org/recording-agent", "", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	rows, err = s.repoSolidtimeProjects(ctx)
	if err != nil {
		t.Fatalf("repoSolidtimeProjects after clear: %v", err)
	}
	if len(rows) != 1 || rows[0].solidtimeProjectID != "" {
		t.Fatalf("got %+v, want the mapping cleared", rows)
	}
}
