package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Project is day 11's long-term memory layer: a folder of chats (e.g. "Gift
// Card") that outlives any single one of them. KnownStack/Notes are kept up
// to date automatically after every turn of every chat in the project (see
// memory_project.go) — unlike Chat.Task (working memory), this state is
// visible to every chat in the project, not just the one that produced it.
// Invariants (day 14, see memory_invariants.go) is a separate, stricter
// list: hard constraints the agent must never violate, not just reference
// memory — extracted with a much higher bar, and never self-removed by the
// agent (only a manual UpdateProjectInvariants call can shrink it).
type Project struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	KnownStack []string  `json:"known_stack,omitempty"`
	Notes      []string  `json:"notes,omitempty"`
	Invariants []string  `json:"invariants,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ProjectStore persists projects as one JSON file per project — the same
// shape as ChatStore, not LabStore's single index file, because a project
// (like a chat) grows content over time and a single corrupt file must not
// take every other project down with it.
type ProjectStore struct {
	dir string
}

func NewProjectStore(dir string) *ProjectStore {
	return &ProjectStore{dir: dir}
}

func (s *ProjectStore) path(projectID string) string {
	return filepath.Join(s.dir, projectID+".json")
}

// Save writes project's full state to disk, creating the store directory on
// first use.
func (s *ProjectStore) Save(project *Project) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(project.ID), data, 0o644)
}

// Delete removes a project's file. A project that was never persisted
// (already gone, or the store directory doesn't exist yet) is not an error.
func (s *ProjectStore) Delete(projectID string) error {
	err := os.Remove(s.path(projectID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// LoadAll reads every persisted project, oldest first by CreatedAt.
func (s *ProjectStore) LoadAll() ([]*Project, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	projects := make([]*Project, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var project Project
		if err := json.Unmarshal(data, &project); err != nil {
			log.Printf("store: skipping %s: %v", entry.Name(), err)
			continue
		}
		projects = append(projects, &project)
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].CreatedAt.Before(projects[j].CreatedAt)
	})
	return projects, nil
}

// ErrProjectNotFound is returned by every Agent method that looks up a
// project by ID.
var ErrProjectNotFound = fmt.Errorf("agent: project not found")

// projectCopy returns a copy of p safe to hand to a caller outside the lock
// — KnownStack/Notes are deep-copied so the caller can't mutate agent state
// through the returned value.
func projectCopy(p *Project) *Project {
	copied := *p
	copied.KnownStack = append([]string(nil), p.KnownStack...)
	copied.Notes = append([]string(nil), p.Notes...)
	copied.Invariants = append([]string(nil), p.Invariants...)
	return &copied
}

// CreateProject starts a new, empty project.
func (a *Agent) CreateProject(name string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("agent: project name must not be empty")
	}

	project := &Project{ID: newChatID(), Name: name, CreatedAt: time.Now()}

	a.mu.Lock()
	a.projects[project.ID] = project
	a.mu.Unlock()

	if err := a.projectStore.Save(project); err != nil {
		log.Printf("agent: failed to persist new project %s: %v", project.ID, err)
	}
	log.Printf("agent: created project %s %q", project.ID, name)
	return projectCopy(project), nil
}

// ListProjects returns every project, oldest first.
func (a *Agent) ListProjects() []*Project {
	a.mu.Lock()
	defer a.mu.Unlock()

	projects := make([]*Project, 0, len(a.projects))
	for _, p := range a.projects {
		projects = append(projects, projectCopy(p))
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].CreatedAt.Before(projects[j].CreatedAt)
	})
	return projects
}

// GetProject returns one project's full state.
func (a *Agent) GetProject(projectID string) (*Project, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	project, ok := a.projects[projectID]
	if !ok {
		return nil, ErrProjectNotFound
	}
	return projectCopy(project), nil
}

// UpdateProjectMemory overwrites a project's long-term memory (known_stack,
// notes) with an explicit, user-authored value — the manual counterpart to
// the automatic per-turn extraction in memory_project.go. Day 11's own
// requirement ("вы явно выбирали, что и куда сохраняется") means this
// memory must be something the user can directly add to, edit, or delete,
// not just watch the model fill in.
func (a *Agent) UpdateProjectMemory(projectID string, knownStack, notes []string) (*Project, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	project, ok := a.projects[projectID]
	if !ok {
		return nil, ErrProjectNotFound
	}
	if knownStack == nil {
		knownStack = []string{}
	}
	if notes == nil {
		notes = []string{}
	}
	project.KnownStack = knownStack
	project.Notes = notes
	if err := a.projectStore.Save(project); err != nil {
		log.Printf("agent: failed to persist project %s after manual memory edit: %v", projectID, err)
	}
	log.Printf("agent: project %s: memory manually edited (%d stack item(s), %d note(s))", projectID, len(knownStack), len(notes))
	return projectCopy(project), nil
}

// UpdateProjectInvariants overwrites a project's invariants (day 14) with an
// explicit, user-authored value — the manual counterpart to the automatic
// per-turn extraction in memory_invariants.go. Unlike KnownStack/Notes,
// invariants are never shrunk by the agent itself (updateInvariantsAfterTurn
// only ever adds), so this manual path is the ONLY way to remove or correct
// one — a safety net against the agent recording something wrong.
func (a *Agent) UpdateProjectInvariants(projectID string, invariants []string) (*Project, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	project, ok := a.projects[projectID]
	if !ok {
		return nil, ErrProjectNotFound
	}
	if invariants == nil {
		invariants = []string{}
	}
	project.Invariants = invariants
	if err := a.projectStore.Save(project); err != nil {
		log.Printf("agent: failed to persist project %s after manual invariants edit: %v", projectID, err)
	}
	log.Printf("agent: project %s: invariants manually edited (%d item(s))", projectID, len(invariants))
	return projectCopy(project), nil
}

// DeleteProject permanently removes a project. Its member chats are kept —
// only their ProjectID is cleared — since a project's chats are real
// task-estimation work, not throwaway comparisons (unlike DeleteLab, which
// cascades: a lab's chats have no purpose outside it).
func (a *Agent) DeleteProject(projectID string) error {
	a.mu.Lock()
	if _, ok := a.projects[projectID]; !ok {
		a.mu.Unlock()
		return ErrProjectNotFound
	}
	delete(a.projects, projectID)
	var orphaned []*Chat
	for _, chat := range a.chats {
		if chat.ProjectID == projectID {
			chat.ProjectID = ""
			orphaned = append(orphaned, chat)
		}
	}
	a.mu.Unlock()

	for _, chat := range orphaned {
		if err := a.store.Save(chat); err != nil {
			log.Printf("agent: failed to persist chat %s after project deletion: %v", chat.ID, err)
		}
	}
	log.Printf("agent: deleted project %s, orphaned %d chat(s)", projectID, len(orphaned))
	return a.projectStore.Delete(projectID)
}
