package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// UserProfile is day 12's personalization layer: a single, global profile
// (there is only ever one — this app has no auth/multi-user concept) applied
// to every non-lab request. Unlike Project (per-group knowledge) or Task
// (per-chat working state), this is the most stable layer: how the user
// wants to be addressed and how they want answers shaped, independent of
// any particular chat or project.
type UserProfile struct {
	Name        string   `json:"name"`
	Style       string   `json:"style"`
	Format      string   `json:"format"`
	Constraints []string `json:"constraints"`
}

// ProfileStore persists the single global profile as one JSON file — unlike
// ChatStore/ProjectStore (one file per entity, keyed by ID), there is only
// ever one profile, so the store is keyed by a fixed path, not an ID.
type ProfileStore struct {
	path string
}

func NewProfileStore(path string) *ProfileStore {
	return &ProfileStore{path: path}
}

// Load reads the persisted profile, or returns an empty (but non-nil-field)
// profile if none has been saved yet — a fresh install has no profile.json
// until the first auto-extraction or manual edit writes one.
func (s *ProfileStore) Load() (*UserProfile, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return &UserProfile{Constraints: []string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var profile UserProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	if profile.Constraints == nil {
		profile.Constraints = []string{}
	}
	return &profile, nil
}

// Save writes profile's full state to disk, creating the parent directory on
// first use.
func (s *ProfileStore) Save(profile *UserProfile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// profileCopy returns a copy of p safe to hand to a caller outside the lock
// — Constraints is deep-copied so the caller can't mutate agent state
// through the returned value.
func profileCopy(p *UserProfile) *UserProfile {
	copied := *p
	copied.Constraints = append([]string(nil), p.Constraints...)
	return &copied
}

// GetProfile returns the current global profile.
func (a *Agent) GetProfile() *UserProfile {
	a.mu.Lock()
	defer a.mu.Unlock()
	return profileCopy(a.profile)
}

// UpdateProfile overwrites the global profile with an explicit, user-authored
// value — the manual counterpart to updateProfileAfterTurn's automatic
// per-turn extraction. This is the primary path for Name in particular: the
// model is instructed never to infer it, only ever confirm what the user
// stated (see profileSystemPrompt) — a mistaken guess here would be a worse
// experience than an empty field, so a direct, explicit edit must always be
// available regardless of what auto-extraction has or hasn't picked up.
func (a *Agent) UpdateProfile(name, style, format string, constraints []string) (*UserProfile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if constraints == nil {
		constraints = []string{}
	}
	a.profile = &UserProfile{
		Name:        name,
		Style:       style,
		Format:      format,
		Constraints: constraints,
	}
	if err := a.profileStore.Save(a.profile); err != nil {
		log.Printf("agent: failed to persist profile after manual edit: %v", err)
	}
	log.Printf("agent: profile manually edited (%d constraint(s))", len(constraints))
	return profileCopy(a.profile), nil
}
