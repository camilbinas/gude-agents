// Package disk provides a file-backed conversation store with atomic CAS.
package disk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

var _ agent.ConversationManager = (*Conversation)(nil)

type Conversation struct {
	dir string
	mu  sync.RWMutex
}

type Option func(*Conversation)

type diskSnapshot struct {
	Messages json.RawMessage `json:"messages"`
	Revision uint64          `json:"revision"`
}

func New(dir string, opts ...Option) (*Conversation, error) {
	if dir == "" {
		return nil, fmt.Errorf("disk conversation: directory path is required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("disk conversation: create directory: %w", err)
	}
	m := &Conversation{dir: dir}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

func (m *Conversation) Save(ctx context.Context, conversationID string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	messageData, err := conversation.MarshalMessages(messages)
	if err != nil {
		return 0, fmt.Errorf("disk conversation: marshal: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	lock, err := m.lock(ctx, true)
	if err != nil {
		return 0, err
	}
	defer unlock(lock)

	current, err := m.loadUnlocked(conversationID)
	if err != nil {
		return 0, err
	}
	if current.Revision != expectedRevision {
		return 0, fmt.Errorf("disk conversation: save %q: expected revision %d, current revision %d: %w", conversationID, expectedRevision, current.Revision, agent.ErrConversationConflict)
	}
	next := expectedRevision + 1
	data, err := json.Marshal(diskSnapshot{Messages: messageData, Revision: next})
	if err != nil {
		return 0, fmt.Errorf("disk conversation: marshal envelope: %w", err)
	}
	if err := writeAtomic(m.path(conversationID), data); err != nil {
		return 0, fmt.Errorf("disk conversation: write: %w", err)
	}
	return next, nil
}

func (m *Conversation) Load(ctx context.Context, conversationID string) (agent.ConversationSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	lock, err := m.lock(ctx, false)
	if err != nil {
		return agent.ConversationSnapshot{}, err
	}
	defer unlock(lock)
	return m.loadUnlocked(conversationID)
}

func (m *Conversation) loadUnlocked(conversationID string) (agent.ConversationSnapshot, error) {
	data, err := os.ReadFile(m.path(conversationID))
	if err != nil {
		if os.IsNotExist(err) {
			return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
		}
		return agent.ConversationSnapshot{}, fmt.Errorf("disk conversation: read: %w", err)
	}
	var stored diskSnapshot
	if err := json.Unmarshal(data, &stored); err != nil || stored.Messages == nil {
		// Files written before CAS stored the raw message array. Treat those
		// snapshots as revision zero so the next Save can upgrade atomically.
		messages, legacyErr := conversation.UnmarshalMessages(data)
		if legacyErr != nil {
			if err != nil {
				return agent.ConversationSnapshot{}, fmt.Errorf("disk conversation: unmarshal envelope: %w", err)
			}
			return agent.ConversationSnapshot{}, fmt.Errorf("disk conversation: unmarshal messages: %w", legacyErr)
		}
		return agent.ConversationSnapshot{Messages: messages}, nil
	}
	messages, err := conversation.UnmarshalMessages(stored.Messages)
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("disk conversation: unmarshal messages: %w", err)
	}
	return agent.ConversationSnapshot{Messages: messages, Revision: stored.Revision}, nil
}

func (m *Conversation) List(ctx context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	lock, err := m.lock(ctx, false)
	if err != nil {
		return nil, err
	}
	defer unlock(lock)
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, fmt.Errorf("disk conversation: list: %w", err)
	}
	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	return ids, nil
}

func (m *Conversation) Delete(ctx context.Context, conversationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, err := m.lock(ctx, true)
	if err != nil {
		return err
	}
	defer unlock(lock)
	if err := os.Remove(m.path(conversationID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disk conversation: delete: %w", err)
	}
	return nil
}

func (m *Conversation) path(conversationID string) string {
	safe := strings.ReplaceAll(conversationID, "/", "_")
	safe = strings.ReplaceAll(safe, "..", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	return filepath.Join(m.dir, safe+".json")
}

func (m *Conversation) lock(ctx context.Context, exclusive bool) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(m.dir, ".conversation.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("disk conversation: open lock: %w", err)
	}
	operation := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		operation = syscall.LOCK_EX | syscall.LOCK_NB
	}
	for {
		if err := syscall.Flock(int(file.Fd()), operation); err == nil {
			return file, nil
		} else if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			file.Close()
			return nil, fmt.Errorf("disk conversation: acquire lock: %w", err)
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func unlock(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func writeAtomic(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".conversation-*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
