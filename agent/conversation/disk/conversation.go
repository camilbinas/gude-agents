// Package disk provides a file-based memory driver for the gude-agents framework.
// It stores each conversation as a JSON file in a directory on the local filesystem.
//
// This is useful for CLI tools, local agents, development, and single-machine
// deployments where you want persistence across restarts without running Redis
// or any external service.
//
// Usage:
//
//	store, err := disk.New("/tmp/agent-memory")
//	// Creates files like /tmp/agent-memory/conv-123.json
package disk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

// Compile-time interface checks.
var _ agent.Conversation = (*DiskConversation)(nil)
var _ conversation.ConversationManager = (*DiskConversation)(nil)

// DiskConversation implements agent.Conversation and conversation.ConversationManager using the
// local filesystem. Each conversation is stored as a JSON file.
type DiskConversation struct {
	dir string
	mu  sync.RWMutex
}

// Option configures a DiskMemory instance.
type Option func(*DiskConversation)

// New creates a new DiskMemory that stores conversations in the given directory.
// The directory is created if it doesn't exist.
func New(dir string, opts ...Option) (*DiskConversation, error) {
	if dir == "" {
		return nil, fmt.Errorf("disk conversation: directory path is required")
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("disk conversation: create directory: %w", err)
	}

	m := &DiskConversation{dir: dir}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

// Save persists messages for the given conversation ID as a JSON file.
func (m *DiskConversation) Save(_ context.Context, conversationID string, messages []agent.Message) error {
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return fmt.Errorf("disk conversation: marshal: %w", err)
	}

	path := m.path(conversationID)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Write to a temp file first, then rename for atomicity.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("disk conversation: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // clean up on failure
		return fmt.Errorf("disk conversation: rename: %w", err)
	}

	return nil
}

// Load retrieves messages for the given conversation ID.
// Returns an empty slice if the file does not exist.
func (m *DiskConversation) Load(_ context.Context, conversationID string) ([]agent.Message, error) {
	path := m.path(conversationID)

	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []agent.Message{}, nil
		}
		return nil, fmt.Errorf("disk conversation: read: %w", err)
	}

	messages, err := conversation.UnmarshalMessages(data)
	if err != nil {
		return nil, fmt.Errorf("disk conversation: unmarshal: %w", err)
	}

	return messages, nil
}

// List returns all conversation IDs in the directory.
func (m *DiskConversation) List(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("disk conversation: list: %w", err)
	}

	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	return ids, nil
}

// Delete removes the conversation file. Returns nil if the file doesn't exist.
func (m *DiskConversation) Delete(_ context.Context, conversationID string) error {
	path := m.path(conversationID)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disk conversation: delete: %w", err)
	}
	return nil
}

// path returns the file path for a conversation ID.
func (m *DiskConversation) path(conversationID string) string {
	// Sanitize the conversation ID to prevent path traversal.
	safe := strings.ReplaceAll(conversationID, "/", "_")
	safe = strings.ReplaceAll(safe, "..", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	return filepath.Join(m.dir, safe+".json")
}
