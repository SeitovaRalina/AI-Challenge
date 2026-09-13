package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ChatStore persists chats as one JSON file per chat, so the agent can
// restore every conversation's full history and current estimate after a
// restart. There is no separate index file — the chat list is reconstructed
// by reading every file in dir, which keeps the on-disk state impossible to
// desync from what's actually there.
type ChatStore struct {
	dir string
}

func NewChatStore(dir string) *ChatStore {
	return &ChatStore{dir: dir}
}

func (s *ChatStore) path(chatID string) string {
	return filepath.Join(s.dir, chatID+".json")
}

// Save writes chat's full state to disk, creating the store directory on
// first use.
func (s *ChatStore) Save(chat *Chat) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(chat, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(chat.ID), data, 0o644)
}

// Delete removes a chat's file. A chat that was never persisted (already
// gone, or the store directory doesn't exist yet) is not an error.
func (s *ChatStore) Delete(chatID string) error {
	err := os.Remove(s.path(chatID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// LoadAll reads every persisted chat, oldest first by CreatedAt, so the
// agent can restore its in-memory state on startup. A missing store
// directory (first run) yields an empty, non-error result.
func (s *ChatStore) LoadAll() ([]*Chat, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	chats := make([]*Chat, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var chat Chat
		if err := json.Unmarshal(data, &chat); err != nil {
			return nil, err
		}
		chats = append(chats, &chat)
	}

	sort.Slice(chats, func(i, j int) bool {
		return chats[i].CreatedAt.Before(chats[j].CreatedAt)
	})
	return chats, nil
}
