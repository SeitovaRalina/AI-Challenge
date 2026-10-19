package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// solidtimeAPI is a thin client over one self-hosted Solidtime instance
// (https://github.com/solidtime-io/solidtime) — Laravel-based, REST,
// Bearer-token auth. The exact endpoints below were found by probing a
// real instance (no official API reference was reachable): there is no
// "list my organizations" endpoint — a personal access token only exposes
// GET /api/v1/users/me/memberships, each membership naming one
// organization; GET .../time-entries additionally requires member_id (the
// membership id, not the user id) or it answers 401 even with a fully-
// scoped token.
type solidtimeAPI struct {
	baseURL    string
	token      string
	httpClient *http.Client

	organizationID string
	memberID       string
}

// newSolidtimeAPI resolves the organization/member once, at construction,
// so a misconfigured token or an unreachable instance fails fast at server
// startup instead of on the first real tool call. organizationIDOverride,
// when non-empty, skips picking the first membership — needed only for an
// account that belongs to more than one organization.
func newSolidtimeAPI(ctx context.Context, baseURL, token, organizationIDOverride string) (*solidtimeAPI, error) {
	api := &solidtimeAPI{baseURL: baseURL, token: token, httpClient: &http.Client{Timeout: 20 * time.Second}}

	var memberships apiMembershipsResponse
	if err := api.get(ctx, "/api/v1/users/me/memberships", nil, &memberships); err != nil {
		return nil, fmt.Errorf("resolve organization: %w", err)
	}
	if len(memberships.Data) == 0 {
		return nil, fmt.Errorf("resolve organization: the token has no organization membership")
	}

	chosen := memberships.Data[0]
	if organizationIDOverride != "" {
		found := false
		for _, m := range memberships.Data {
			if m.Organization.ID == organizationIDOverride {
				chosen, found = m, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("resolve organization: SOLIDTIME_ORGANIZATION_ID %q is not among this token's memberships", organizationIDOverride)
		}
	} else if len(memberships.Data) > 1 {
		// Not fatal — just picking the first deterministically — but worth
		// surfacing, since a silent wrong-organization pick would be
		// confusing to debug later.
		fmt.Printf("solidtime: token belongs to %d organizations, using the first (%s); set SOLIDTIME_ORGANIZATION_ID to pick another\n", len(memberships.Data), chosen.Organization.ID)
	}

	api.organizationID = chosen.Organization.ID
	api.memberID = chosen.ID
	return api, nil
}

type apiMembership struct {
	ID           string `json:"id"`
	Organization struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"organization"`
	Role string `json:"role"`
}

type apiMembershipsResponse struct {
	Data []apiMembership `json:"data"`
}

// ProjectInfo is one Solidtime project, trimmed to what the timesheet
// audit and its project-mapping UI need.
type ProjectInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsArchived bool   `json:"is_archived"`
	IsBillable bool   `json:"is_billable"`
}

type apiProject struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsArchived bool   `json:"is_archived"`
	IsBillable bool   `json:"is_billable"`
}

type apiProjectsResponse struct {
	Data []apiProject `json:"data"`
}

func (a *solidtimeAPI) projects(ctx context.Context) ([]ProjectInfo, error) {
	var resp apiProjectsResponse
	path := fmt.Sprintf("/api/v1/organizations/%s/projects", a.organizationID)
	if err := a.get(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]ProjectInfo, len(resp.Data))
	for i, p := range resp.Data {
		out[i] = ProjectInfo{ID: p.ID, Name: p.Name, IsArchived: p.IsArchived, IsBillable: p.IsBillable}
	}
	return out, nil
}

// TimeEntryInfo is one already-tracked Solidtime time entry, trimmed to
// what the timesheet audit's duplicate-overlap check needs.
type TimeEntryInfo struct {
	ID              string    `json:"id"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	DurationSeconds int       `json:"duration_seconds"`
	ProjectID       string    `json:"project_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	Billable        bool      `json:"billable"`
	Description     string    `json:"description"`
}

type apiTimeEntry struct {
	ID          string     `json:"id"`
	Start       time.Time  `json:"start"`
	End         *time.Time `json:"end"`
	Duration    *int       `json:"duration"`
	ProjectID   string     `json:"project_id"`
	TaskID      string     `json:"task_id"`
	Billable    bool       `json:"billable"`
	Description string     `json:"description"`
}

type apiTimeEntriesResponse struct {
	Data []apiTimeEntry `json:"data"`
	Meta struct {
		Total int `json:"total"`
	} `json:"meta"`
}

// timeEntries lists this member's own time entries in [from, to) — a
// running timer (no end yet) is skipped, since it has no end to compare
// intervals against and isn't a "draft vs already-tracked" duplicate risk.
func (a *solidtimeAPI) timeEntries(ctx context.Context, from, to time.Time) ([]TimeEntryInfo, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s/time-entries", a.organizationID)
	query := url.Values{
		"member_id": {a.memberID},
		"start":     {from.UTC().Format(time.RFC3339)},
		"end":       {to.UTC().Format(time.RFC3339)},
		"limit":     {"500"},
	}
	var all []apiTimeEntry
	for offset := 0; ; offset += 500 {
		query.Set("offset", strconv.Itoa(offset))
		var page apiTimeEntriesResponse
		if err := a.get(ctx, path, query, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if len(page.Data) < 500 || (page.Meta.Total > 0 && len(all) >= page.Meta.Total) {
			break
		}
	}
	out := make([]TimeEntryInfo, 0, len(all))
	for _, e := range all {
		if e.End == nil {
			continue
		}
		duration := 0
		if e.Duration != nil {
			duration = *e.Duration
		}
		out = append(out, TimeEntryInfo{ID: e.ID, Start: e.Start, End: *e.End, DurationSeconds: duration, ProjectID: e.ProjectID, TaskID: e.TaskID, Billable: e.Billable, Description: e.Description})
	}
	return out, nil
}

type TaskInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	IsDone    bool   `json:"is_done"`
}

func (a *solidtimeAPI) tasks(ctx context.Context) ([]TaskInfo, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s/tasks", a.organizationID)
	var tasks []TaskInfo
	for offset := 0; ; offset += 500 {
		query := url.Values{"limit": {"500"}, "offset": {strconv.Itoa(offset)}}
		var page struct {
			Data []TaskInfo `json:"data"`
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		if err := a.get(ctx, path, query, &page); err != nil {
			return nil, err
		}
		tasks = append(tasks, page.Data...)
		if len(page.Data) < 500 || (page.Meta.Total > 0 && len(tasks) >= page.Meta.Total) {
			break
		}
	}
	return tasks, nil
}

type CreateTimeEntry struct {
	Start       time.Time
	End         time.Time
	ProjectID   string
	TaskID      string
	Description string
	Billable    bool
}

func (a *solidtimeAPI) createTimeEntry(ctx context.Context, in CreateTimeEntry) (TimeEntryInfo, error) {
	path := fmt.Sprintf("/api/v1/organizations/%s/time-entries", a.organizationID)
	payload := map[string]any{
		"member_id":   a.memberID,
		"start":       in.Start.UTC().Format("2006-01-02T15:04:05Z"),
		"end":         in.End.UTC().Format("2006-01-02T15:04:05Z"),
		"project_id":  in.ProjectID,
		"task_id":     in.TaskID,
		"description": in.Description,
		"billable":    in.Billable,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return TimeEntryInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return TimeEntryInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return TimeEntryInfo{}, fmt.Errorf("create time entry: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TimeEntryInfo{}, fmt.Errorf("read create response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return TimeEntryInfo{}, fmt.Errorf("create time entry: status %d: %s", resp.StatusCode, truncate(string(responseBody), 300))
	}
	var result struct {
		Data apiTimeEntry `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return TimeEntryInfo{}, fmt.Errorf("decode create response: %w", err)
	}
	if result.Data.ID == "" {
		return TimeEntryInfo{}, fmt.Errorf("create response missing entry id")
	}
	return TimeEntryInfo{ID: result.Data.ID, Start: result.Data.Start, End: in.End, ProjectID: in.ProjectID, TaskID: in.TaskID, Description: in.Description, Billable: in.Billable}, nil
}

func (a *solidtimeAPI) get(ctx context.Context, path string, query url.Values, out any) error {
	u := a.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response from %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %s: %s", path, strconv.Itoa(resp.StatusCode), truncate(string(body), 300))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
