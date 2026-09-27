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
)

// Kind is the normalized type of one activity event.
type Kind string

const (
	KindCommit       Kind = "commit"
	KindPROpened     Kind = "pr_opened"
	KindPRMerged     Kind = "pr_merged"
	KindReview       Kind = "review"
	KindIssueComment Kind = "issue_comment"
)

// ActivityEvent is the product's source-agnostic unit of work activity.
type ActivityEvent struct {
	ID         string    `json:"id" jsonschema:"stable unique id, e.g. github:commit:<sha>"`
	Source     string    `json:"source" jsonschema:"always github for this server"`
	Kind       Kind      `json:"kind" jsonschema:"commit, pr_opened, pr_merged, review or issue_comment"`
	Repo       string    `json:"repo" jsonschema:"owner/repo"`
	Title      string    `json:"title" jsonschema:"commit subject, PR title or comment excerpt"`
	URL        string    `json:"url" jsonschema:"link to the event on github.com"`
	OccurredAt time.Time `json:"occurred_at" jsonschema:"when the event happened (RFC3339)"`
	Author     string    `json:"author" jsonschema:"GitHub login of the author"`
	Ref        string    `json:"ref,omitempty" jsonschema:"branch name for commits, #number for PR/review/comment events"`
}

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
		Description: "Get the authenticated user's own GitHub activity for a period of at most 31 days: commits (on every branch pushed in the period), pull requests opened and merged, reviews submitted and issue/PR comments. Returns normalized events, oldest first, with per-kind counts. Use it to answer questions about what the user worked on.",
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
	since, err := parseBound(in.Since, false)
	if err != nil {
		return nil, GetActivityOutput{}, fmt.Errorf("since: %w", err)
	}
	until := now
	if in.Until != "" {
		if until, err = parseBound(in.Until, true); err != nil {
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
		want := map[string]bool{}
		for _, r := range in.Repos {
			want[strings.ToLower(strings.TrimSpace(r))] = true
		}
		filtered := repos[:0]
		for _, r := range repos {
			if want[strings.ToLower(r.FullName)] {
				filtered = append(filtered, r)
				delete(want, strings.ToLower(r.FullName))
			}
		}
		repos = filtered
		for name := range want {
			warnings = append(warnings, fmt.Sprintf("%s: не входит в отслеживаемые репозитории", name))
		}
	}

	// Repos are independent — fetch a few at a time rather than one by one.
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		events []ActivityEvent
		sem    = make(chan struct{}, 4)
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

	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	out := GetActivityOutput{
		Login:    login,
		Since:    since,
		Until:    until,
		Timezone: localZoneName(),
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

// parseBound accepts an RFC3339 timestamp or a bare YYYY-MM-DD date in the
// server's local timezone — the start of that day for since, its end for
// until, so "until: 2026-09-26" includes all of the 26th.
func parseBound(s string, endOfDay bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("is required")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is neither RFC3339 nor YYYY-MM-DD", s)
	}
	if endOfDay {
		return d.AddDate(0, 0, 1).Add(-time.Second), nil
	}
	return d, nil
}

func localZoneName() string {
	name, offset := time.Now().Zone()
	return fmt.Sprintf("%s (UTC%+03d:%02d)", name, offset/3600, abs(offset%3600)/60)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
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
