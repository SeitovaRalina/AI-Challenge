package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fakeTimesheetMCP struct {
	session  sessionRaw
	entry    map[string]any
	posts    int
	failPost bool
}

func assignMCP(out any, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (f *fakeTimesheetMCP) worklog(tool string, _ map[string]any, out any) error {
	switch tool {
	case "get_sessions":
		return assignMCP(out, map[string]any{"sessions": []sessionRaw{f.session}})
	case "get_repo_solidtime_projects":
		return assignMCP(out, map[string]any{"repos": []map[string]string{{"repo": "owner/repo", "solidtime_project_id": "project-1"}}})
	case "list_events":
		return assignMCP(out, map[string]any{"events": []any{}})
	default:
		return errors.New("unexpected worklog tool: " + tool)
	}
}

func (f *fakeTimesheetMCP) solidtime(tool string, _ map[string]any, out any) error {
	switch tool {
	case "list_projects":
		return assignMCP(out, map[string]any{"projects": []map[string]any{{"id": "project-1", "name": "Project", "is_billable": true}}})
	case "list_tasks":
		return assignMCP(out, map[string]any{"tasks": []map[string]any{{"id": "task-1", "project_id": "project-1", "name": "Task"}, {"id": "task-other", "project_id": "project-2", "name": "Wrong"}}})
	case "list_time_entries":
		if f.entry != nil {
			return assignMCP(out, map[string]any{"entries": []any{f.entry}})
		}
		return assignMCP(out, map[string]any{"entries": []any{}})
	case "create_time_entry":
		f.posts++
		if f.failPost {
			return errors.New("connection lost")
		}
		return assignMCP(out, map[string]any{"entry": map[string]string{"id": "entry-1"}})
	default:
		return errors.New("unexpected Solidtime tool: " + tool)
	}
}

func newFakeTimesheetService(t *testing.T, f *fakeTimesheetMCP) *TimesheetSyncService {
	t.Helper()
	s, err := NewTimesheetSyncService(nil, nil, nil, filepath.Join(t.TempDir(), "drafts.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.worklogCall = f.worklog
	s.solidtimeCall = f.solidtime
	return s
}

func TestTimesheetConfirmAndSyncOnce(t *testing.T) {
	start := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	f := &fakeTimesheetMCP{session: sessionRaw{Start: start, End: start.Add(time.Hour), Kind: "work", Repo: "owner/repo"}}
	s := newFakeTimesheetService(t, f)
	prepared, err := s.prepare(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Drafts) != 1 || prepared.Drafts[0].Status != "draft" {
		t.Fatalf("unexpected drafts: %+v", prepared.Drafts)
	}
	id := prepared.Drafts[0].ID
	if _, err := s.review(context.Background(), id, TimesheetReviewInput{Action: "confirm", TaskID: "task-other", Description: "Work"}); err == nil {
		t.Fatal("task from another project accepted")
	}
	confirmed, err := s.review(context.Background(), id, TimesheetReviewInput{Action: "confirm", TaskID: "task-1", Description: "  Сделала задачу  ", Billable: true})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Description != "Сделала задачу" || !confirmed.Billable {
		t.Fatalf("review was not saved: %+v", confirmed)
	}
	synced, err := s.syncOne(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if synced.Status != "synced" || synced.SolidtimeEntryID != "entry-1" || f.posts != 1 {
		t.Fatalf("unexpected sync: %+v, posts=%d", synced, f.posts)
	}
	if _, err := s.syncOne(context.Background(), id); err == nil || f.posts != 1 {
		t.Fatal("second POST was not blocked")
	}
	reloaded, err := NewTimesheetSyncService(nil, nil, nil, s.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.list(); len(got) != 1 || got[0].Status != "synced" {
		t.Fatalf("status was not persisted: %+v", got)
	}
}

func TestTimesheetAmbiguousWriteStaysBlocked(t *testing.T) {
	start := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	f := &fakeTimesheetMCP{session: sessionRaw{Start: start, End: start.Add(time.Hour), Kind: "work", Repo: "owner/repo"}, failPost: true}
	s := newFakeTimesheetService(t, f)
	prepared, err := s.prepare(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	id := prepared.Drafts[0].ID
	if _, err := s.review(context.Background(), id, TimesheetReviewInput{Action: "confirm", TaskID: "task-1", Description: "Сделала задачу"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.syncOne(context.Background(), id); err == nil {
		t.Fatal("lost POST response accepted")
	}
	if got := s.list()[0].Status; got != "syncing" {
		t.Fatalf("status=%s", got)
	}
	if _, err := s.syncOne(context.Background(), id); err == nil || f.posts != 1 {
		t.Fatal("ambiguous POST retried")
	}
	if _, err := s.reconcile(context.Background(), id); err == nil {
		t.Fatal("missing entry marked synced")
	}
	f.entry = map[string]any{"id": "entry-1", "start": f.session.Start, "end": f.session.End, "project_id": "project-1", "task_id": "task-1", "description": "Сделала задачу"}
	got, err := s.reconcile(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "synced" || got.SolidtimeEntryID != "entry-1" || f.posts != 1 {
		t.Fatalf("reconcile=%+v posts=%d", got, f.posts)
	}
}

func TestTimesheetDuplicateNeverPosted(t *testing.T) {
	start := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	f := &fakeTimesheetMCP{session: sessionRaw{Start: start, End: start.Add(time.Hour), Kind: "work", Repo: "owner/repo"}}
	s := newFakeTimesheetService(t, f)
	prepared, err := s.prepare(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	id := prepared.Drafts[0].ID
	if _, err := s.review(context.Background(), id, TimesheetReviewInput{Action: "confirm", TaskID: "task-1", Description: "Work"}); err != nil {
		t.Fatal(err)
	}
	f.entry = map[string]any{"id": "existing", "start": start.Add(30 * time.Minute), "end": start.Add(90 * time.Minute)}
	if _, err := s.syncOne(context.Background(), id); err == nil || f.posts != 0 {
		t.Fatal("duplicate posted")
	}
}
