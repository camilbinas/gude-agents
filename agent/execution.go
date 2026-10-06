package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ExecutionStatus is the durable lifecycle state of an invocation.
type ExecutionStatus string

const (
	ExecutionRunning   ExecutionStatus = "running"
	ExecutionPaused    ExecutionStatus = "paused"
	ExecutionCompleted ExecutionStatus = "completed"
	ExecutionFailed    ExecutionStatus = "failed"
	ExecutionCanceled  ExecutionStatus = "canceled"
)

// ExecutionPhase describes the current engine section. It is intentionally
// small: this first durable model does not attempt durable tool boundaries.
type ExecutionPhase string

const (
	ExecutionPhaseModel  ExecutionPhase = "model"
	ExecutionPhaseTools  ExecutionPhase = "tools"
	ExecutionPhasePaused ExecutionPhase = "paused"
	ExecutionPhaseDone   ExecutionPhase = "done"
)

// ExecutionPause is the small runtime payload needed to project a paused
// execution as an Interrupt. Conversation history always remains in the
// ConversationStore and is referenced by the execution cursor.
type ExecutionPause struct {
	Type     InterruptType      `json:"type"`
	Approval *ApprovalInterrupt `json:"approval,omitempty"`
	Input    *InputInterrupt    `json:"input,omitempty"`
}

// Execution is durable runtime state for one invocation. Version is an
// ExecutionStore CAS version, distinct from the ConversationStore revision.
type Execution struct {
	ID string `json:"id"`

	ConversationID string `json:"conversation_id,omitempty"`
	Revision       uint64 `json:"revision"`
	LastSequence   uint64 `json:"last_sequence"`

	Status    ExecutionStatus `json:"status"`
	Phase     ExecutionPhase  `json:"phase"`
	Iteration int             `json:"iteration"`
	Pause     *ExecutionPause `json:"pause,omitempty"`
	Usage     TokenUsage      `json:"usage"`
	Version   uint64          `json:"version"`
}

// ExecutionStore persists small durable execution state. Create is create-only;
// Save uses Version as a CAS token. Implementations must not store transcript
// messages or conversation snapshots.
type ExecutionStore interface {
	Create(ctx context.Context, execution Execution) (Execution, error)
	Load(ctx context.Context, executionID string) (Execution, error)
	Save(ctx context.Context, execution Execution, expectedVersion uint64) (Execution, error)
}

var (
	ErrExecutionNotFound = errors.New("execution not found")
	ErrExecutionConflict = errors.New("execution version conflict")
	ErrNoExecutionStore  = errors.New("no execution store configured")
)

// memoryExecutionStore is private same-process replay protection. It is used
// only when no durable ExecutionStore is configured and may retain a stateless
// snapshot in interrupt.Messages; it is not public persistence infrastructure.
type memoryExecutionStore struct {
	mu    sync.Mutex
	items map[string]Execution
}

func newMemoryExecutionStore() *memoryExecutionStore {
	return &memoryExecutionStore{items: make(map[string]Execution)}
}

func (s *memoryExecutionStore) Create(_ context.Context, execution Execution) (Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if execution.ID == "" {
		return Execution{}, fmt.Errorf("execution ID is required")
	}
	if _, exists := s.items[execution.ID]; exists {
		return Execution{}, fmt.Errorf("execution %q: %w", execution.ID, ErrExecutionConflict)
	}
	execution.Version = 1
	execution = cloneExecution(execution)
	s.items[execution.ID] = execution
	return cloneExecution(execution), nil
}

func (s *memoryExecutionStore) Load(_ context.Context, id string) (Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, ok := s.items[id]
	if !ok {
		return Execution{}, fmt.Errorf("execution %q: %w", id, ErrExecutionNotFound)
	}
	return cloneExecution(execution), nil
}

func (s *memoryExecutionStore) Save(_ context.Context, execution Execution, expectedVersion uint64) (Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.items[execution.ID]
	if !ok {
		return Execution{}, fmt.Errorf("execution %q: %w", execution.ID, ErrExecutionNotFound)
	}
	if stored.Version != expectedVersion {
		return Execution{}, fmt.Errorf("execution %q: %w", execution.ID, ErrExecutionConflict)
	}
	execution.Version = expectedVersion + 1
	execution = cloneExecution(execution)
	s.items[execution.ID] = execution
	return cloneExecution(execution), nil
}

func cloneExecution(in Execution) Execution {
	out := in
	if in.Pause != nil {
		pause := *in.Pause
		if in.Pause.Approval != nil {
			approval := *in.Pause.Approval
			approval.Calls = append([]ApprovalCall(nil), approval.Calls...)
			for i := range approval.Calls {
				approval.Calls[i].Input = cloneRaw(approval.Calls[i].Input)
			}
			pause.Approval = &approval
		}
		if in.Pause.Input != nil {
			input := *in.Pause.Input
			pause.Input = &input
		}
		out.Pause = &pause
	}
	return out
}

func newExecutionID(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	var bytes [16]byte
	if _, err := io.ReadFull(r, bytes[:]); err != nil {
		return "", fmt.Errorf("generate execution ID: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
