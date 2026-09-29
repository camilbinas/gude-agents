package conversation

import (
	"context"
	"fmt"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// InMemory is a simple in-process conversation manager backed by a map.
var _ agent.ConversationManager = (*InMemory)(nil)

type memoryEntry struct {
	messages []agent.Message
	revision uint64
}

type InMemory struct {
	mu   sync.RWMutex
	data map[string]memoryEntry
}

// NewInMemory creates a new empty in-memory conversation store.
func NewInMemory() *InMemory {
	return &InMemory{data: make(map[string]memoryEntry)}
}

// Load returns a deep copy of the current snapshot. Missing conversations have
// revision zero and a non-nil empty message slice.
func (m *InMemory) Load(_ context.Context, id string) (agent.ConversationSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.data[id]
	if !ok {
		return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
	}
	return agent.ConversationSnapshot{Messages: deepCopyMessages(entry.messages), Revision: entry.revision}, nil
}

// Save atomically commits messages when expectedRevision matches.
func (m *InMemory) Save(_ context.Context, id string, msgs []agent.Message, expectedRevision uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.data[id]
	current := uint64(0)
	if ok {
		current = entry.revision
	}
	if current != expectedRevision {
		return 0, fmt.Errorf("conversation %q: expected revision %d, current revision %d: %w", id, expectedRevision, current, agent.ErrConversationConflict)
	}
	next := current + 1
	m.data[id] = memoryEntry{messages: deepCopyMessages(msgs), revision: next}
	return next, nil
}

func (m *InMemory) List(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.data))
	for id := range m.data {
		ids = append(ids, id)
	}
	return ids, nil
}

func (m *InMemory) Delete(_ context.Context, conversationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, conversationID)
	return nil
}

func deepCopyMessages(msgs []agent.Message) []agent.Message {
	cp := make([]agent.Message, len(msgs))
	for i, message := range msgs {
		content := make([]agent.ContentBlock, len(message.Content))
		for j, block := range message.Content {
			switch block := block.(type) {
			case agent.ToolUseBlock:
				block.Input = append([]byte(nil), block.Input...)
				content[j] = block
			case agent.ToolResultBlock:
				block.Images = append([]agent.ImageBlock(nil), block.Images...)
				for k := range block.Images {
					block.Images[k].Source.Data = append([]byte(nil), block.Images[k].Source.Data...)
				}
				content[j] = block
			case agent.ImageBlock:
				block.Source.Data = append([]byte(nil), block.Source.Data...)
				content[j] = block
			case agent.DocumentBlock:
				block.Source.Data = append([]byte(nil), block.Source.Data...)
				content[j] = block
			case agent.WidgetBlock:
				block.Payload = append([]byte(nil), block.Payload...)
				content[j] = block
			default:
				content[j] = block
			}
		}
		cp[i] = agent.Message{Role: message.Role, Content: content}
	}
	return cp
}
