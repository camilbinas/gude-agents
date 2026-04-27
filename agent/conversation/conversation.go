package conversation

import (
	"context"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// InMemory is a simple in-process Conversation store backed by a map.
type InMemory struct {
	mu   sync.RWMutex
	data map[string][]agent.Message
}

// NewInMemory creates a new empty in-memory conversation store.
func NewInMemory() *InMemory {
	return &InMemory{data: make(map[string][]agent.Message)}
}

// Load retrieves messages for the given conversation ID.
// Returns a deep copy to prevent mutation of the stored data.
func (m *InMemory) Load(_ context.Context, id string) ([]agent.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	msgs := m.data[id]
	return deepCopyMessages(msgs), nil
}

// Save persists messages for the given conversation ID.
// Stores a deep copy to prevent mutation of the stored data.
func (m *InMemory) Save(_ context.Context, id string, msgs []agent.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[id] = deepCopyMessages(msgs)
	return nil
}

// List returns all conversation IDs in the store.
func (m *InMemory) List(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.data))
	for id := range m.data {
		ids = append(ids, id)
	}
	return ids, nil
}

// Delete removes a conversation by ID. Returns nil if not found.
func (m *InMemory) Delete(_ context.Context, conversationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, conversationID)
	return nil
}

// deepCopyMessages returns a deep copy of a message slice.
func deepCopyMessages(msgs []agent.Message) []agent.Message {
	cp := make([]agent.Message, len(msgs))
	for i, m := range msgs {
		content := make([]agent.ContentBlock, len(m.Content))
		copy(content, m.Content)
		cp[i] = agent.Message{
			Role:    m.Role,
			Content: content,
		}
	}
	return cp
}
