package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreateTimeEntryChecksProjectTaskDuplicateAndPayload(t *testing.T) {
	var posts int
	var overlap bool
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer token")
		}
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/projects"):
			fmt.Fprint(w, `{"data":[{"id":"project-1","name":"Project","is_archived":false,"is_billable":true}]}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tasks"):
			fmt.Fprint(w, `{"data":[{"id":"task-1","project_id":"project-1","name":"Development","is_done":false}]}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/time-entries"):
			if overlap {
				fmt.Fprint(w, `{"data":[{"id":"existing","start":"2026-10-01T03:30:00Z","end":"2026-10-01T04:30:00Z"}]}`)
			} else {
				fmt.Fprint(w, `{"data":[]}`)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/time-entries"):
			posts++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["member_id"] != "member-1" || payload["project_id"] != "project-1" || payload["task_id"] != "task-1" || payload["description"] != "Реализация проверки" || payload["billable"] != true || payload["start"] != "2026-10-01T03:00:00Z" || payload["end"] != "2026-10-01T04:00:00Z" {
				t.Errorf("unexpected create payload: %v", payload)
			}
			fmt.Fprint(w, `{"data":{"id":"created-1","start":"2026-10-01T03:00:00Z","end":"2026-10-01T04:00:00Z"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer httpServer.Close()
	s := &server{api: &solidtimeAPI{baseURL: httpServer.URL, token: "test-token", httpClient: httpServer.Client(), organizationID: "org-1", memberID: "member-1"}}
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.FixedZone("Omsk", 6*3600))
	input := CreateTimeEntryInput{Start: start.Format(time.RFC3339), End: start.Add(time.Hour).Format(time.RFC3339), ProjectID: "project-1", TaskID: "wrong-task", Description: "Реализация проверки", Billable: true}
	if _, _, err := s.createTimeEntry(context.Background(), nil, input); err == nil {
		t.Fatal("accepted task outside project")
	}
	input.TaskID = "task-1"
	overlap = true
	if _, _, err := s.createTimeEntry(context.Background(), nil, input); err == nil {
		t.Fatal("accepted overlapping entry")
	}
	overlap = false
	_, result, err := s.createTimeEntry(context.Background(), nil, input)
	if err != nil || posts != 1 || result.Entry.ID != "created-1" {
		t.Fatalf("posts=%d result=%+v err=%v", posts, result, err)
	}
}
