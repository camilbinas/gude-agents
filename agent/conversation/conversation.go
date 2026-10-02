package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// InMemory is an in-process append-only conversation manager. It deep-copies
// at both the storage boundary and read boundary, so callers cannot mutate a
// committed event through an alias.
var (
	_ agent.ConversationManager = (*InMemory)(nil)
	_ agent.ContextStateStore   = (*InMemory)(nil)
)

type memoryContextState struct {
	data     json.RawMessage
	revision uint64
}

type memoryEntry struct {
	messages     []agent.Message
	revision     uint64
	lastSequence uint64
	contextState map[string]memoryContextState
}

type InMemory struct {
	mu   sync.RWMutex
	data map[string]memoryEntry
}

func NewInMemory() *InMemory { return &InMemory{data: make(map[string]memoryEntry)} }

func emptySnapshot() agent.ConversationSnapshot {
	return agent.ConversationSnapshot{Messages: []agent.Message{}}
}

// Load returns the complete canonical transcript.
func (m *InMemory) Load(_ context.Context, id string) (agent.ConversationSnapshot, error) {
	return m.LoadAfter(context.Background(), id, 0)
}

// LoadAfter returns the contiguous canonical suffix after afterSequence.
func (m *InMemory) LoadAfter(_ context.Context, id string, afterSequence uint64) (agent.ConversationSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.data[id]
	if !ok {
		return emptySnapshot(), nil
	}
	start := afterSequence
	if start > entry.lastSequence {
		start = entry.lastSequence
	}
	return agent.ConversationSnapshot{
		Messages:     deepCopyMessages(entry.messages[start:]),
		Revision:     entry.revision,
		LastSequence: entry.lastSequence,
	}, nil
}

// Append atomically appends all messages or none. Empty batches are checked
// no-ops and do not materialize a missing conversation.
func (m *InMemory) Append(_ context.Context, id string, msgs []agent.Message, expectedRevision uint64) (agent.ConversationCursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, exists := m.data[id]
	if entry.revision != expectedRevision {
		return agent.ConversationCursor{}, fmt.Errorf("conversation %q: expected revision %d, current revision %d: %w", id, expectedRevision, entry.revision, agent.ErrConversationConflict)
	}
	cursor := agent.ConversationCursor{Revision: entry.revision, LastSequence: entry.lastSequence}
	if len(msgs) == 0 {
		return cursor, nil
	}
	if !exists {
		entry.contextState = make(map[string]memoryContextState)
	}
	entry.messages = append(entry.messages, deepCopyMessages(msgs)...)
	entry.lastSequence += uint64(len(msgs))
	entry.revision++
	m.data[id] = entry
	return agent.ConversationCursor{Revision: entry.revision, LastSequence: entry.lastSequence}, nil
}

func (m *InMemory) LoadContextState(_ context.Context, id, key string) (agent.ContextStateSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.data[id].contextState[key]
	if !ok {
		return agent.ContextStateSnapshot{}, nil
	}
	return agent.ContextStateSnapshot{Data: append(json.RawMessage(nil), state.data...), Revision: state.revision}, nil
}

func (m *InMemory) SaveContextState(_ context.Context, id, key string, data json.RawMessage, expectedRevision uint64) (uint64, error) {
	if !json.Valid(data) {
		return 0, fmt.Errorf("context state %q: invalid JSON", key)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.data[id]
	if entry.contextState == nil {
		entry.contextState = make(map[string]memoryContextState)
	}
	current := entry.contextState[key]
	if current.revision != expectedRevision {
		return 0, fmt.Errorf("context state %q: expected revision %d, current revision %d: %w", key, expectedRevision, current.revision, agent.ErrContextStateConflict)
	}
	current.revision++
	current.data = append(json.RawMessage(nil), data...)
	entry.contextState[key] = current
	// Context state alone deliberately does not create a listable conversation.
	if entry.revision != 0 || entry.lastSequence != 0 || len(entry.messages) != 0 {
		m.data[id] = entry
	} else {
		// Store state without creating canonical activity; it remains reachable
		// by explicit ID and is removed by Delete.
		m.data[id] = entry
	}
	return current.revision, nil
}

func (m *InMemory) List(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.data))
	for id, entry := range m.data {
		if entry.revision > 0 {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (m *InMemory) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, id)
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
