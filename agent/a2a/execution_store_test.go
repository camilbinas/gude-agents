package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// testExecutionStore is a minimal in-memory agent.ExecutionStore for tests that
// need a pausable agent. Production code selects a store explicitly (for
// example agent/execution), but this module's tests do not depend on that
// module so the published dependency graph stays unchanged.
type testExecutionStore struct {
	mu    sync.Mutex
	items map[string]agent.Execution
}

func newTestExecutionStore() *testExecutionStore {
	return &testExecutionStore{items: make(map[string]agent.Execution)}
}

func cloneTestExecution(in agent.Execution) agent.Execution {
	data, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	var out agent.Execution
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	out.Version = in.Version
	return out
}

func (s *testExecutionStore) Create(_ context.Context, execution agent.Execution) (agent.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[execution.ID]; ok {
		return agent.Execution{}, fmt.Errorf("execution %q: %w", execution.ID, agent.ErrExecutionConflict)
	}
	execution.Version = 1
	s.items[execution.ID] = cloneTestExecution(execution)
	return cloneTestExecution(execution), nil
}

func (s *testExecutionStore) Load(_ context.Context, id string) (agent.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, ok := s.items[id]
	if !ok {
		return agent.Execution{}, fmt.Errorf("execution %q: %w", id, agent.ErrExecutionNotFound)
	}
	return cloneTestExecution(execution), nil
}

func (s *testExecutionStore) Save(_ context.Context, execution agent.Execution, expectedVersion uint64) (agent.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.items[execution.ID]
	if !ok || current.Version != expectedVersion {
		return agent.Execution{}, fmt.Errorf("execution %q: %w", execution.ID, agent.ErrExecutionConflict)
	}
	execution.Version = expectedVersion + 1
	s.items[execution.ID] = cloneTestExecution(execution)
	return cloneTestExecution(execution), nil
}
