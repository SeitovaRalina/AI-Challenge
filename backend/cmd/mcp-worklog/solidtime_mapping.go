package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Day 32: a repository's Solidtime project is set explicitly, never guessed
// by matching names — repo_projects (sessions.go) already showed that a
// repository and its product-side project label can differ, and a
// repository and its Solidtime project name differ even more freely (e.g.
// repo "Recording-Agent" tracked in Solidtime as "Project Internal:
// Agents"). The timesheet audit (backend/timesheet_audit.go) treats an
// unmapped repository as "needs a human decision", not as a guess to make
// on its own.

// ---- set_repo_solidtime_project / get_repo_solidtime_projects ----

type SetRepoSolidtimeProjectInput struct {
	Repo                 string `json:"repo" jsonschema:"owner/repo, as it appears in stored events"`
	SolidtimeProjectID   string `json:"solidtime_project_id" jsonschema:"the Solidtime project's id (see mcp-solidtime's list_projects); empty clears the mapping"`
	SolidtimeProjectName string `json:"solidtime_project_name,omitempty" jsonschema:"the Solidtime project's name, for display without a second lookup — ignored when solidtime_project_id is empty"`
}

type SetRepoSolidtimeProjectOutput struct {
	Repo                 string `json:"repo"`
	SolidtimeProjectID   string `json:"solidtime_project_id,omitempty"`
	SolidtimeProjectName string `json:"solidtime_project_name,omitempty"`
}

func (s *server) setRepoSolidtimeProjectTool(ctx context.Context, _ *mcp.CallToolRequest, in SetRepoSolidtimeProjectInput) (*mcp.CallToolResult, SetRepoSolidtimeProjectOutput, error) {
	repo := in.Repo
	if repo == "" {
		return nil, SetRepoSolidtimeProjectOutput{}, errors.New("repo is required")
	}
	name := in.SolidtimeProjectName
	if in.SolidtimeProjectID == "" {
		name = ""
	}
	if err := s.store.setRepoSolidtimeProject(ctx, repo, in.SolidtimeProjectID, name); err != nil {
		return nil, SetRepoSolidtimeProjectOutput{}, fmt.Errorf("store: %w", err)
	}
	return nil, SetRepoSolidtimeProjectOutput{Repo: repo, SolidtimeProjectID: in.SolidtimeProjectID, SolidtimeProjectName: name}, nil
}

type GetRepoSolidtimeProjectsInput struct{}

type RepoSolidtimeProjectView struct {
	Repo                 string `json:"repo"`
	SolidtimeProjectID   string `json:"solidtime_project_id,omitempty" jsonschema:"empty means not explicitly mapped yet"`
	SolidtimeProjectName string `json:"solidtime_project_name,omitempty"`
}

type GetRepoSolidtimeProjectsOutput struct {
	Repos []RepoSolidtimeProjectView `json:"repos"`
}

func (s *server) getRepoSolidtimeProjectsTool(ctx context.Context, _ *mcp.CallToolRequest, _ GetRepoSolidtimeProjectsInput) (*mcp.CallToolResult, GetRepoSolidtimeProjectsOutput, error) {
	rows, err := s.store.repoSolidtimeProjects(ctx)
	if err != nil {
		return nil, GetRepoSolidtimeProjectsOutput{}, err
	}
	out := GetRepoSolidtimeProjectsOutput{Repos: make([]RepoSolidtimeProjectView, len(rows))}
	for i, r := range rows {
		out.Repos[i] = RepoSolidtimeProjectView{Repo: r.repo, SolidtimeProjectID: r.solidtimeProjectID, SolidtimeProjectName: r.solidtimeProjectName}
	}
	return nil, out, nil
}
