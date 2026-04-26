package conversation

import (
	"context"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// InMemory is a simple in-process Conversation store backed by a map.
// Documented in docs/conversation.md — update when changing methods or thread-safety guarantees.
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
func (s *InMemory) Load(_ context.Context, id string) ([]agent.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := s.data[id]
	return deepCopyMessages(msgs), nil
}

// Save persists messages for the given conversation ID.
// Stores a deep copy to prevent mutation of the stored data.
func (s *InMemory) Save(_ context.Context, id string, msgs []agent.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = deepCopyMessages(msgs)
	return nil
}

// List returns all conversation IDs in the store.
func (s *InMemory) List(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.data))
	for id := range s.data {
		ids = append(ids, id)
	}
	return ids, nil
}

// Delete removes a conversation by ID. Returns nil if not found.
func (s *InMemory) Delete(_ context.Context, conversationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, conversationID)
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
