// Command mcp-github is the product's own GitHub Activity MCP server: a thin,
// stateless adapter that turns GitHub REST data into the product's
// normalized ActivityEvent shape. It speaks MCP over stdio — the backend
// starts it as a subprocess — and exposes two read-only tools:
//
//   - list_repos   — which repositories activity is collected from
//   - get_activity — the authenticated user's own events for a period
//
// Configuration comes from the environment (the backend passes its own):
// GITHUB_TOKEN (required) and GITHUB_REPOS (optional, comma-separated
// owner/repo; when empty, every repo the token can see is used).
// Stdout carries the MCP protocol, so all logging goes to stderr.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aiwork/backend/internal/activity"
)

// The event shape is shared with the Worklog MCP server, which stores what
// this one returns (day 18).
type (
	Kind          = activity.Kind
	ActivityEvent = activity.Event
)

const (
	KindCommit       = activity.KindCommit
	KindPROpened     = activity.KindPROpened
	KindPRMerged     = activity.KindPRMerged
	KindReview       = activity.KindReview
	KindIssueComment = activity.KindIssueComment
)

// maxWindow and maxEvents keep a single call bounded — both in GitHub API
// requests and in how much text lands in the model's context.
const (
	maxWindow = 31 * 24 * time.Hour
	maxEvents = 500
)

type GetActivityInput struct {
	Since string   `json:"since" jsonschema:"start of the period: RFC3339 timestamp with offset, or a YYYY-MM-DD date (start of that day, server local time)"`
	Until string   `json:"until,omitempty" jsonschema:"end of the period: RFC3339 timestamp, or a YYYY-MM-DD date (end of that day, server local time); defaults to now"`
	Repos []string `json:"repos,omitempty" jsonschema:"only these repositories (owner/repo); defaults to every tracked repository"`
}

type GetActivityOutput struct {
	Login     string          `json:"login" jsonschema:"GitHub login the activity belongs to"`
	Since     time.Time       `json:"since"`
	Until     time.Time       `json:"until"`
	Timezone  string          `json:"timezone" jsonschema:"server local timezone used to interpret dates"`
	Repos     []string        `json:"repos" jsonschema:"repositories that were checked"`
	Events    []ActivityEvent `json:"events" jsonschema:"events, oldest first"`
	Counts    map[Kind]int    `json:"counts" jsonschema:"number of events per kind"`
	Truncated bool            `json:"truncated" jsonschema:"true when more than 500 events matched and the rest were dropped"`
	Warnings  []string        `json:"warnings,omitempty" jsonschema:"repositories or endpoints that could not be read"`
}

type ListReposInput struct{}

type RepoInfo struct {
	Repo          string    `json:"repo" jsonschema:"owner/repo"`
	Private       bool      `json:"private"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at" jsonschema:"last push to any branch"`
	URL           string    `json:"url"`
}

type ListReposOutput struct {
	Login    string     `json:"login"`
	Source   string     `json:"source" jsonschema:"configured (GITHUB_REPOS) or token (every repo the token can see)"`
	Repos    []RepoInfo `json:"repos"`
	Warnings []string   `json:"warnings,omitempty"`
}

type server struct {
	api        *githubAPI
	configured []string
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("mcp-github: ")
	// Standalone runs (go run ./cmd/mcp-github) read backend/.env; when the
	// backend spawns this process it already passes the same variables.
	_ = godotenv.Load()

	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		log.Fatal("GITHUB_TOKEN is not set")
	}
	s := &server{api: newGitHubAPI(token), configured: splitRepos(os.Getenv("GITHUB_REPOS"))}

	srv := mcp.NewServer(&mcp.Implementation{Name: "aiwork-github-activity", Title: "GitHub Activity", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: "Read-only access to the authenticated user's own GitHub work activity (commits, pull requests, reviews, comments), normalized into ActivityEvent records.",
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_repos",
		Title:       "Отслеживаемые репозитории",
		Description: "List the GitHub repositories whose activity is tracked for the user, with visibility, default branch and last push time.",
		Annotations: readOnly,
	}, s.listRepos)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_activity",
		Title:       "Активность в GitHub за период",
		Description: "Get the authenticated user's own GitHub activity for a period of at most 31 days: commits (on every branch pushed in the period), pull requests opened and merged, reviews submitted and issue/PR comments. Returns normalized events, oldest first, with per-kind counts. Use it only to answer questions about what the user worked on — never to estimate a new task: estimates do not use activity data.",
		Annotations: readOnly,
	}, s.getActivity)

	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func (s *server) listRepos(ctx context.Context, _ *mcp.CallToolRequest, _ ListReposInput) (*mcp.CallToolResult, ListReposOutput, error) {
	login, err := s.api.Login(ctx)
	if err != nil {
		return nil, ListReposOutput{}, fmt.Errorf("github: cannot resolve the token's user: %w", err)
	}
	repos, warnings := s.api.Repos(ctx, s.configured)
	out := ListReposOutput{Login: login, Source: "token", Repos: []RepoInfo{}, Warnings: warnings}
	if len(s.configured) > 0 {
		out.Source = "configured"
	}
	for _, r := range repos {
		out.Repos = append(out.Repos, RepoInfo{Repo: r.FullName, Private: r.Private, DefaultBranch: r.DefaultBranch, PushedAt: r.PushedAt, URL: r.HTMLURL})
	}
	return nil, out, nil
}

func (s *server) getActivity(ctx context.Context, _ *mcp.CallToolRequest, in GetActivityInput) (*mcp.CallToolResult, GetActivityOutput, error) {
	now := time.Now()
	since, err := activity.ParseBound(in.Since, false)
	if err != nil {
		return nil, GetActivityOutput{}, fmt.Errorf("since: %w", err)
	}
	until := now
	if in.Until != "" {
		if until, err = activity.ParseBound(in.Until, true); err != nil {
			return nil, GetActivityOutput{}, fmt.Errorf("until: %w", err)
		}
	}
	if until.After(now) {
		until = now
	}
	if !until.After(since) {
		return nil, GetActivityOutput{}, errors.New("until must be after since")
	}
	if until.Sub(since) > maxWindow {
		return nil, GetActivityOutput{}, errors.New("the period must not exceed 31 days")
	}

	login, err := s.api.Login(ctx)
	if err != nil {
		return nil, GetActivityOutput{}, fmt.Errorf("github: cannot resolve the token's user: %w", err)
	}

	repos, warnings := s.api.Repos(ctx, s.configured)
	if len(in.Repos) > 0 {
		var unmatched []string
		repos, unmatched = filterRepos(repos, in.Repos)
		warnings = append(warnings, unmatched...)
	}

	// Repos are independent — fetch several at a time rather than one by one.
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		events []ActivityEvent
		sem    = make(chan struct{}, 8)
	)
	for _, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ev, w := s.api.Activity(ctx, repo, login, since, until)
			mu.Lock()
			events = append(events, ev...)
			warnings = append(warnings, w...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Report times in the server's local timezone (the user's), with the
	// offset, rather than GitHub's UTC: the model otherwise converts them
	// itself and was observed putting a just-after-midnight merge on the
	// previous day.
	for i := range events {
		events[i].OccurredAt = events[i].OccurredAt.In(time.Local)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	out := GetActivityOutput{
		Login:    login,
		Since:    since,
		Until:    until,
		Timezone: activity.LocalZoneName(),
		Repos:    make([]string, 0, len(repos)),
		Counts:   map[Kind]int{},
		Warnings: warnings,
	}
	for _, r := range repos {
		out.Repos = append(out.Repos, r.FullName)
	}
	if len(events) > maxEvents {
		events = events[len(events)-maxEvents:]
		out.Truncated = true
	}
	out.Events = events
	if out.Events == nil {
		out.Events = []ActivityEvent{}
	}
	for _, e := range events {
		out.Counts[e.Kind]++
	}
	log.Printf("get_activity %s..%s: %d repo(s), %d event(s), %d warning(s)",
		since.Format(time.RFC3339), until.Format(time.RFC3339), len(repos), len(events), len(warnings))
	return nil, out, nil
}


func splitRepos(s string) []string {
	var repos []string
	for _, r := range strings.Split(s, ",") {
		if r = strings.TrimSpace(r); r != "" {
			repos = append(repos, r)
		}
	}
	return repos
}

// filterRepos keeps the tracked repos the caller asked for. A name may be
// the full owner/repo or — since people (and models) often say just
// "AI-Challenge" — the bare repo name, as long as it's unambiguous.
func filterRepos(repos []ghRepo, names []string) (matched []ghRepo, warnings []string) {
	picked := map[string]bool{}
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		var hits []ghRepo
		for _, r := range repos {
			full := strings.ToLower(r.FullName)
			if full == name || (!strings.Contains(name, "/") && strings.HasSuffix(full, "/"+name)) {
				hits = append(hits, r)
			}
		}
		switch {
		case len(hits) == 0:
			warnings = append(warnings, fmt.Sprintf("%s: не входит в отслеживаемые репозитории", raw))
		case len(hits) > 1:
			warnings = append(warnings, fmt.Sprintf("%s: неоднозначно, укажите owner/repo", raw))
		case !picked[hits[0].FullName]:
			picked[hits[0].FullName] = true
			matched = append(matched, hits[0])
		}
	}
	return matched, warnings
}
