package execution

import (
	"context"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/record"
)

var _ agent.ExecutionStore = (*Memory)(nil)

// Memory is an agent.ExecutionStore that keeps execution state in the memory of
// the running process.
//
// It is intended for tests, development, and examples. Every execution,
// including paused ones, is lost when the process exits. Nothing is shared
// between processes or between Memory values, and nothing is ever evicted, so a
// long-running service accumulates state until it restarts. Use one of the
// durable stores (agent/execution/postgres, agent/execution/redis,
// agent/execution/dynamodb) when a pause must survive a restart.
//
// A paused run also needs a ConversationStore that owns its transcript; pair
// Memory with a conversation store whose durability matches your needs.
type Memory struct {
	mu    sync.Mutex
	items map[string]memoryRecord
}

// memoryRecord holds the serialized payload, exactly as a durable backend
// would, so the memory store exercises the same serialization path.
type memoryRecord struct {
	version uint64
	payload []byte
}

// NewMemory returns an empty, independent, process-local ExecutionStore.
func NewMemory() *Memory {
	return &Memory{items: make(map[string]memoryRecord)}
}

// Create stores a new execution at version 1. The ID must be absent.
func (m *Memory) Create(_ context.Context, execution agent.Execution) (agent.Execution, error) {
	if err := record.ValidateID(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	payload, err := record.Marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.items[execution.ID]; exists {
		return agent.Execution{}, record.Conflict("create", execution.ID)
	}
	m.items[execution.ID] = memoryRecord{version: 1, payload: payload}
	return record.Unmarshal(payload, execution.ID, 1)
}

// Load returns the current execution.
func (m *Memory) Load(_ context.Context, id string) (agent.Execution, error) {
	if err := record.ValidateID(id); err != nil {
		return agent.Execution{}, err
	}
	m.mu.Lock()
	stored, ok := m.items[id]
	m.mu.Unlock()
	if !ok {
		return agent.Execution{}, record.NotFound(id)
	}
	return record.Unmarshal(stored.payload, id, stored.version)
}

// Save replaces the execution when expectedVersion is exactly the current
// version, and returns it at the next version.
func (m *Memory) Save(_ context.Context, execution agent.Execution, expectedVersion uint64) (agent.Execution, error) {
	if err := record.ValidateID(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	if err := record.CheckExpected(execution.ID, expectedVersion); err != nil {
		return agent.Execution{}, err
	}
	payload, err := record.Marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.items[execution.ID]
	if !ok || current.version != expectedVersion {
		return agent.Execution{}, record.Conflict("save", execution.ID)
	}
	next := expectedVersion + 1
	m.items[execution.ID] = memoryRecord{version: next, payload: payload}
	return record.Unmarshal(payload, execution.ID, next)
}
