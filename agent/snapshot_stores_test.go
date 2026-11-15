package agent

import (
	"context"
	"encoding/json"
	"sync"
)

// snapshotConvStore is a minimal append-only ConversationStore. It deep-copies at
// both boundaries, mirroring conversation.InMemory, and enforces the revision
// CAS so Restore keeps the same create-only guarantees as a real store.
type snapshotConvStore struct {
	mu   sync.RWMutex
	data map[string]snapshotConvEntry
}

type snapshotConvEntry struct {
	messages     []Message
	revision     uint64
	lastSequence uint64
}

func newSnapshotConvStore() *snapshotConvStore {
	return &snapshotConvStore{data: make(map[string]snapshotConvEntry)}
}

func (m *snapshotConvStore) Load(_ context.Context, id string) (ConversationSnapshot, error) {
	return m.LoadAfter(context.Background(), id, 0)
}

func (m *snapshotConvStore) LoadAfter(_ context.Context, id string, after uint64) (ConversationSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.data[id]
	if !ok {
		return ConversationSnapshot{Messages: []Message{}}, nil
	}
	start := after
	if start > entry.lastSequence {
		start = entry.lastSequence
	}
	return ConversationSnapshot{
		Messages:     cloneMessages(entry.messages[start:]),
		Revision:     entry.revision,
		LastSequence: entry.lastSequence,
	}, nil
}

func (m *snapshotConvStore) Append(_ context.Context, id string, msgs []Message, expectedRevision uint64) (ConversationCursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.data[id]
	if entry.revision != expectedRevision {
		return ConversationCursor{}, ErrConversationConflict
	}
	cursor := ConversationCursor{Revision: entry.revision, LastSequence: entry.lastSequence}
	if len(msgs) == 0 {
		return cursor, nil
	}
	entry.messages = append(entry.messages, cloneMessages(msgs)...)
	entry.lastSequence += uint64(len(msgs))
	entry.revision++
	m.data[id] = entry
	return ConversationCursor{Revision: entry.revision, LastSequence: entry.lastSequence}, nil
}

// snapshotExecStore is a minimal ExecutionStore with the same create-only and
// exact-version CAS semantics the durable backends promise.
type snapshotExecStore struct {
	mu    sync.Mutex
	items map[string]Execution
}

func newSnapshotExecStore() *snapshotExecStore {
	return &snapshotExecStore{items: make(map[string]Execution)}
}

func (m *snapshotExecStore) Create(_ context.Context, e Execution) (Execution, error) {
	if e.ID == "" {
		return Execution{}, ErrExecutionConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[e.ID]; ok {
		return Execution{}, ErrExecutionConflict
	}
	e.Version = 1
	m.items[e.ID] = cloneExecution(e)
	return cloneExecution(e), nil
}

func (m *snapshotExecStore) Load(_ context.Context, id string) (Execution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[id]
	if !ok {
		return Execution{}, ErrExecutionNotFound
	}
	return cloneExecution(e), nil
}

func (m *snapshotExecStore) Save(_ context.Context, e Execution, expected uint64) (Execution, error) {
	if expected == 0 {
		return Execution{}, ErrExecutionConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.items[e.ID]
	if !ok {
		return Execution{}, ErrExecutionNotFound
	}
	if current.Version != expected {
		return Execution{}, ErrExecutionConflict
	}
	e.Version = expected + 1
	m.items[e.ID] = cloneExecution(e)
	return cloneExecution(e), nil
}

// cloneMessages deep-copies messages so a stored event cannot be mutated
// through a caller alias.
func cloneMessages(msgs []Message) []Message {
	cp := make([]Message, len(msgs))
	for i, message := range msgs {
		content := make([]ContentBlock, len(message.Content))
		for j, block := range message.Content {
			switch b := block.(type) {
			case ToolUseBlock:
				b.Input = append(json.RawMessage(nil), b.Input...)
				content[j] = b
			case ToolResultBlock:
				b.Images = append([]ImageBlock(nil), b.Images...)
				for k := range b.Images {
					b.Images[k].Source.Data = append([]byte(nil), b.Images[k].Source.Data...)
				}
				content[j] = b
			case ImageBlock:
				b.Source.Data = append([]byte(nil), b.Source.Data...)
				content[j] = b
			case DocumentBlock:
				b.Source.Data = append([]byte(nil), b.Source.Data...)
				content[j] = b
			case WidgetBlock:
				b.Payload = append(json.RawMessage(nil), b.Payload...)
				content[j] = b
			default:
				content[j] = block
			}
		}
		cp[i] = Message{Role: message.Role, Content: content}
	}
	return cp
}

// withTestPauseStores gives a Runtime fresh in-memory conversation and
// execution stores.
func withTestPauseStores() Option {
	return func(a *Runtime) error {
		a.conversation = newSnapshotConvStore()
		a.executionStore = newSnapshotExecStore()
		return nil
	}
}
