package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// githubAPI is a minimal read-only GitHub REST client: just the handful of
// endpoints get_activity/list_repos need, nothing generic.
type githubAPI struct {
	token string
	base  string
	http  *http.Client

	loginOnce sync.Once
	login     string
	loginErr  error

	// The repo list changes rarely but is needed on every call — cache it
	// briefly rather than re-listing on each get_activity.
	reposMu       sync.Mutex
	reposCached   []ghRepo
	reposWarnings []string
	reposAt       time.Time
}

const reposCacheTTL = 5 * time.Minute

func newGitHubAPI(token string) *githubAPI {
	return &githubAPI{token: token, base: "https://api.github.com", http: &http.Client{Timeout: 20 * time.Second}}
}

func (g *githubAPI) get(ctx context.Context, path string, query url.Values, out any) error {
	u := g.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("GET %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Login is the authenticated user, resolved once per server process — every
// activity filter ("my commits", "my PRs") is relative to it.
func (g *githubAPI) Login(ctx context.Context) (string, error) {
	g.loginOnce.Do(func() {
		var u struct {
			Login string `json:"login"`
		}
		g.loginErr = g.get(ctx, "/user", nil, &u)
		g.login = u.Login
	})
	return g.login, g.loginErr
}

type ghRepo struct {
	FullName      string    `json:"full_name"`
	Private       bool      `json:"private"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	HTMLURL       string    `json:"html_url"`
}

// Repos returns the configured repos (GITHUB_REPOS), or — when none are
// configured — every repo the token can see, most recently pushed first.
func (g *githubAPI) Repos(ctx context.Context, configured []string) ([]ghRepo, []string) {
	g.reposMu.Lock()
	defer g.reposMu.Unlock()
	if g.reposCached != nil && time.Since(g.reposAt) < reposCacheTTL {
		return append([]ghRepo(nil), g.reposCached...), append([]string(nil), g.reposWarnings...)
	}
	repos, warnings := g.listRepos(ctx, configured)
	if len(repos) > 0 {
		g.reposCached, g.reposWarnings, g.reposAt = repos, warnings, time.Now()
	}
	return append([]ghRepo(nil), repos...), warnings
}

func (g *githubAPI) listRepos(ctx context.Context, configured []string) ([]ghRepo, []string) {
	var warnings []string
	if len(configured) > 0 {
		repos := make([]ghRepo, 0, len(configured))
		for _, name := range configured {
			var r ghRepo
			if err := g.get(ctx, "/repos/"+name, nil, &r); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: недоступен (%v)", name, err))
				continue
			}
			repos = append(repos, r)
		}
		return repos, warnings
	}

	var repos []ghRepo
	for page := 1; page <= 5; page++ {
		var batch []ghRepo
		q := url.Values{"per_page": {"100"}, "sort": {"pushed"}, "page": {fmt.Sprint(page)}}
		if err := g.get(ctx, "/user/repos", q, &batch); err != nil {
			warnings = append(warnings, fmt.Sprintf("список репозиториев: %v", err))
			break
		}
		repos = append(repos, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return repos, warnings
}

// mergeCommitTitle matches the "(#123)" suffix GitHub puts on a squash/merge
// commit's subject line.
var mergeCommitTitle = regexp.MustCompile(`\(#\d+\)\s*$`)

// Activity collects login's own events in repo within [since, until].
// Errors on individual endpoints degrade to warnings, so one inaccessible
// endpoint never hides everything else the repo has.
func (g *githubAPI) Activity(ctx context.Context, repo ghRepo, login string, since, until time.Time) ([]ActivityEvent, []string) {
	var (
		events   []ActivityEvent
		warnings []string
	)
	warn := func(what string, err error) {
		warnings = append(warnings, fmt.Sprintf("%s: %s: %v", repo.FullName, what, err))
	}

	// Commits need a push, so a repo last pushed before the window can't
	// have any — skip the per-branch walk entirely for it.
	if !repo.PushedAt.Before(since) {
		ev, err := g.commits(ctx, repo, login, since, until)
		if err != nil {
			warn("коммиты", err)
		}
		events = append(events, ev...)
	}

	ev, err := g.pullRequests(ctx, repo, login, since, until)
	if err != nil {
		warn("pull requests", err)
	}
	events = append(events, ev...)

	ev, err = g.issueComments(ctx, repo, login, since, until)
	if err != nil {
		warn("комментарии", err)
	}
	events = append(events, ev...)

	return events, warnings
}

// maxBranches bounds the per-branch commit walk for a repo with a very
// large number of branches.
const maxBranches = 30

func (g *githubAPI) commits(ctx context.Context, repo ghRepo, login string, since, until time.Time) ([]ActivityEvent, error) {
	var branches []struct {
		Name string `json:"name"`
	}
	if err := g.get(ctx, "/repos/"+repo.FullName+"/branches", url.Values{"per_page": {"100"}}, &branches); err != nil {
		return nil, err
	}
	// Default branch first, so a commit reachable from several branches is
	// attributed to it rather than to whichever feature branch came first.
	sort.SliceStable(branches, func(i, j int) bool {
		return branches[i].Name == repo.DefaultBranch && branches[j].Name != repo.DefaultBranch
	})
	if len(branches) > maxBranches {
		branches = branches[:maxBranches]
	}

	type ghCommit struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
		Commit  struct {
			Message string `json:"message"`
			Author  struct {
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
		Committer *struct {
			Login string `json:"login"`
		} `json:"committer"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}

	// One request per branch, fetched concurrently; results are then merged
	// in branch order (default branch first) so dedup stays deterministic.
	perBranch := make([][]ghCommit, len(branches))
	errs := make([]error, len(branches))
	forEachLimited(len(branches), branchConcurrency, func(i int) {
		q := url.Values{
			"sha":      {branches[i].Name},
			"author":   {login},
			"since":    {since.UTC().Format(time.RFC3339)},
			"until":    {until.UTC().Format(time.RFC3339)},
			"per_page": {"100"},
		}
		errs[i] = g.get(ctx, "/repos/"+repo.FullName+"/commits", q, &perBranch[i])
	})

	seen := map[string]bool{}
	var events []ActivityEvent
	var firstErr error
	for i, b := range branches {
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		for _, c := range perBranch[i] {
			// A rebased or cherry-picked copy of a commit gets a new sha but
			// keeps its message and author date — it is the same piece of
			// work, so it's deduped on those too, not on sha alone (seen
			// live: pre-rebase copies merged back in doubled a week's count).
			copyKey := c.Commit.Message + "\x00" + c.Commit.Author.Date.UTC().Format(time.RFC3339)
			if seen[c.SHA] || seen[copyKey] {
				continue
			}
			seen[c.SHA] = true
			seen[copyKey] = true
			title := firstLine(c.Commit.Message)
			// Merge commits (two parents) and the squash commit GitHub
			// creates when a PR is merged are not separate work — the
			// pr_merged event already records the merge, and the branch's
			// own commits record the actual work.
			if len(c.Parents) > 1 {
				continue
			}
			if c.Committer != nil && c.Committer.Login == "web-flow" && mergeCommitTitle.MatchString(title) {
				continue
			}
			events = append(events, ActivityEvent{
				ID:         "github:commit:" + c.SHA,
				Source:     "github",
				Kind:       KindCommit,
				Repo:       repo.FullName,
				Title:      title,
				URL:        c.HTMLURL,
				OccurredAt: c.Commit.Author.Date,
				Author:     login,
				Ref:        b.Name,
			})
		}
	}
	return events, firstErr
}

// maxReviewLookups bounds how many PRs get a per-PR reviews request.
const maxReviewLookups = 30

func (g *githubAPI) pullRequests(ctx context.Context, repo ghRepo, login string, since, until time.Time) ([]ActivityEvent, error) {
	type ghPull struct {
		Number    int        `json:"number"`
		Title     string     `json:"title"`
		HTMLURL   string     `json:"html_url"`
		CreatedAt time.Time  `json:"created_at"`
		UpdatedAt time.Time  `json:"updated_at"`
		MergedAt  *time.Time `json:"merged_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}

	var inWindow []ghPull
	for page := 1; page <= 5; page++ {
		var pulls []ghPull
		q := url.Values{"state": {"all"}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {"50"}, "page": {fmt.Sprint(page)}}
		if err := g.get(ctx, "/repos/"+repo.FullName+"/pulls", q, &pulls); err != nil {
			return nil, err
		}
		done := len(pulls) < 50
		for _, p := range pulls {
			if p.UpdatedAt.Before(since) {
				done = true
				break
			}
			inWindow = append(inWindow, p)
		}
		if done {
			break
		}
	}

	inRange := func(t time.Time) bool { return !t.Before(since) && !t.After(until) }
	var events []ActivityEvent
	for _, p := range inWindow {
		title := fmt.Sprintf("#%d %s", p.Number, p.Title)
		ref := fmt.Sprintf("#%d", p.Number)
		if p.User.Login == login && inRange(p.CreatedAt) {
			events = append(events, ActivityEvent{
				ID: fmt.Sprintf("github:pr_opened:%s#%d", repo.FullName, p.Number), Source: "github", Kind: KindPROpened,
				Repo: repo.FullName, Title: title, URL: p.HTMLURL, OccurredAt: p.CreatedAt, Author: login, Ref: ref,
			})
		}
		// merged_at, not the list endpoint's "merged" flag — the latter is
		// not reliably populated on list responses.
		if p.User.Login == login && p.MergedAt != nil && inRange(*p.MergedAt) {
			events = append(events, ActivityEvent{
				ID: fmt.Sprintf("github:pr_merged:%s#%d", repo.FullName, p.Number), Source: "github", Kind: KindPRMerged,
				Repo: repo.FullName, Title: title, URL: p.HTMLURL, OccurredAt: *p.MergedAt, Author: login, Ref: ref,
			})
		}
	}

	type ghReview struct {
		ID          int64     `json:"id"`
		State       string    `json:"state"`
		HTMLURL     string    `json:"html_url"`
		SubmittedAt time.Time `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	lookups := min(len(inWindow), maxReviewLookups)
	perPull := make([][]ghReview, lookups)
	errs := make([]error, lookups)
	forEachLimited(lookups, branchConcurrency, func(i int) {
		path := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo.FullName, inWindow[i].Number)
		errs[i] = g.get(ctx, path, url.Values{"per_page": {"100"}}, &perPull[i])
	})

	var firstErr error
	for i := range lookups {
		p := inWindow[i]
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		for _, r := range perPull[i] {
			if r.User.Login != login || !inRange(r.SubmittedAt) {
				continue
			}
			events = append(events, ActivityEvent{
				ID: fmt.Sprintf("github:review:%d", r.ID), Source: "github", Kind: KindReview,
				Repo: repo.FullName, Title: fmt.Sprintf("Review (%s) #%d %s", strings.ToLower(r.State), p.Number, p.Title),
				URL: r.HTMLURL, OccurredAt: r.SubmittedAt, Author: login, Ref: fmt.Sprintf("#%d", p.Number),
			})
		}
	}
	return events, firstErr
}

var issueNumber = regexp.MustCompile(`/(\d+)$`)

func (g *githubAPI) issueComments(ctx context.Context, repo ghRepo, login string, since, until time.Time) ([]ActivityEvent, error) {
	var comments []struct {
		ID        int64     `json:"id"`
		Body      string    `json:"body"`
		HTMLURL   string    `json:"html_url"`
		IssueURL  string    `json:"issue_url"`
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	q := url.Values{"since": {since.UTC().Format(time.RFC3339)}, "per_page": {"100"}}
	if err := g.get(ctx, "/repos/"+repo.FullName+"/issues/comments", q, &comments); err != nil {
		return nil, err
	}
	var events []ActivityEvent
	for _, c := range comments {
		if c.User.Login != login || c.CreatedAt.Before(since) || c.CreatedAt.After(until) {
			continue
		}
		ref := ""
		if m := issueNumber.FindStringSubmatch(c.IssueURL); m != nil {
			ref = "#" + m[1]
		}
		events = append(events, ActivityEvent{
			ID: fmt.Sprintf("github:issue_comment:%d", c.ID), Source: "github", Kind: KindIssueComment,
			Repo: repo.FullName, Title: strings.TrimSpace("Комментарий " + ref + ": " + truncate(firstLine(c.Body), 100)),
			URL: c.HTMLURL, OccurredAt: c.CreatedAt, Author: login, Ref: ref,
		})
	}
	return events, nil
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
}

func truncate(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// branchConcurrency bounds parallel requests within one repository (per
// branch, per PR); repositories themselves run in parallel on top of this.
const branchConcurrency = 6

// forEachLimited runs fn(0..n-1) with at most limit calls in flight.
func forEachLimited(n, limit int, fn func(i int)) {
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}()
	}
	wg.Wait()
}
