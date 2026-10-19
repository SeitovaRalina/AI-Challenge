// Command mcp-solidtime is the product's own Solidtime MCP server (day 32):
// a thin, read-only adapter over one self-hosted Solidtime instance
// (https://github.com/solidtime-io/solidtime) — "what's already tracked
// there", for the timesheet audit to check a draft against. It exposes two
// tools:
//
//   - list_projects     — every project in the configured organization
//   - list_time_entries — this account's own time entries in a period
//
// Configuration comes from the environment (the backend passes its own):
// SOLIDTIME_BASE_URL and SOLIDTIME_API_TOKEN are required; SOLIDTIME_ORGANIZATION_ID
// is only needed when the token belongs to more than one organization.
// Writing to Solidtime (creating a time entry once a draft is confirmed)
// is a later day's addition — this server never does it.
//
// Stdout carries the MCP protocol, so all logging goes to stderr.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aiwork/backend/internal/activity"
)

const maxWindow = 90 * 24 * time.Hour

type ListProjectsInput struct{}

type ListProjectsOutput struct {
	Projects []ProjectInfo `json:"projects"`
}

type ListTimeEntriesInput struct {
	From string `json:"from" jsonschema:"start of the period: RFC3339 timestamp, or a YYYY-MM-DD date (start of that day, server local time)"`
	To   string `json:"to,omitempty" jsonschema:"end of the period; defaults to now"`
}

type ListTimeEntriesOutput struct {
	From     time.Time       `json:"from"`
	To       time.Time       `json:"to"`
	Timezone string          `json:"timezone"`
	Entries  []TimeEntryInfo `json:"entries"`
	Total    int             `json:"total"`
}

type server struct {
	api *solidtimeAPI
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("mcp-solidtime: ")
	_ = godotenv.Load()

	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("SOLIDTIME_BASE_URL")), "/")
	token := strings.TrimSpace(os.Getenv("SOLIDTIME_API_TOKEN"))
	if baseURL == "" || token == "" {
		log.Fatal("SOLIDTIME_BASE_URL and SOLIDTIME_API_TOKEN must be set")
	}
	orgOverride := strings.TrimSpace(os.Getenv("SOLIDTIME_ORGANIZATION_ID"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	api, err := newSolidtimeAPI(ctx, baseURL, token, orgOverride)
	cancel()
	if err != nil {
		log.Fatalf("solidtime: %v", err)
	}
	s := &server{api: api}

	srv := mcp.NewServer(&mcp.Implementation{Name: "aiwork-solidtime", Title: "Solidtime", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: "Read-only access to one self-hosted Solidtime instance's own projects and already-tracked time entries.",
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_projects",
		Title:       "Проекты Solidtime",
		Description: "Every project in the configured Solidtime organization, with its id (needed to map a repository to it — see the Worklog server's set_repo_solidtime_project) and whether it's archived.",
		Annotations: readOnly,
	}, s.listProjects)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_time_entries",
		Title:       "Записи времени в Solidtime",
		Description: "This account's own Solidtime time entries for a period of at most 90 days — what's already tracked, used to check a draft timesheet for possible duplicates before anything is written. Never creates or modifies anything.",
		Annotations: readOnly,
	}, s.listTimeEntries)

	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func (s *server) listProjects(ctx context.Context, _ *mcp.CallToolRequest, _ ListProjectsInput) (*mcp.CallToolResult, ListProjectsOutput, error) {
	projects, err := s.api.projects(ctx)
	if err != nil {
		return nil, ListProjectsOutput{}, fmt.Errorf("solidtime: %w", err)
	}
	if projects == nil {
		projects = []ProjectInfo{}
	}
	return nil, ListProjectsOutput{Projects: projects}, nil
}

func (s *server) listTimeEntries(ctx context.Context, _ *mcp.CallToolRequest, in ListTimeEntriesInput) (*mcp.CallToolResult, ListTimeEntriesOutput, error) {
	now := time.Now()
	since, err := activity.ParseBound(in.From, false)
	if err != nil {
		return nil, ListTimeEntriesOutput{}, fmt.Errorf("from: %w", err)
	}
	until := now
	if in.To != "" {
		if until, err = activity.ParseBound(in.To, true); err != nil {
			return nil, ListTimeEntriesOutput{}, fmt.Errorf("to: %w", err)
		}
	}
	if until.After(now) {
		until = now
	}
	if !until.After(since) {
		return nil, ListTimeEntriesOutput{}, fmt.Errorf("to must be after from")
	}
	if until.Sub(since) > maxWindow {
		return nil, ListTimeEntriesOutput{}, fmt.Errorf("the period must not exceed 90 days")
	}

	entries, err := s.api.timeEntries(ctx, since, until)
	if err != nil {
		return nil, ListTimeEntriesOutput{}, fmt.Errorf("solidtime: %w", err)
	}
	if entries == nil {
		entries = []TimeEntryInfo{}
	}
	log.Printf("list_time_entries %s..%s: %d entr(y/ies)", since.Format(time.RFC3339), until.Format(time.RFC3339), len(entries))
	return nil, ListTimeEntriesOutput{From: since, To: until, Timezone: activity.LocalZoneName(), Entries: entries, Total: len(entries)}, nil
}
