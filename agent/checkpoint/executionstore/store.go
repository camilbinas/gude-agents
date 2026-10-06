// Package executionstore adapts a generic checkpoint.Checkpointer to the
// agent.ExecutionStore semantic API. Execution payloads contain runtime state
// and conversation cursors only; transcript ownership remains with
// ConversationStore.
package executionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
)

var _ agent.ExecutionStore = (*Store)(nil)

const DefaultThreadPrefix = "execution:"
const checkpointLabel = "execution"

type Store struct {
	cp     checkpoint.Checkpointer
	prefix string
}

type Option func(*Store)

func WithThreadPrefix(prefix string) Option { return func(s *Store) { s.prefix = prefix } }

func New(cp checkpoint.Checkpointer, opts ...Option) *Store {
	s := &Store{cp: cp, prefix: DefaultThreadPrefix}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Store) Create(ctx context.Context, execution agent.Execution) (agent.Execution, error) {
	if err := s.validate(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	data, err := marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	cp, err := s.cp.SaveIfVersion(ctx, s.threadID(execution.ID), checkpoint.Checkpoint{
		Label: checkpointLabel,
		Usage: execution.Usage,
		Extra: data,
	}, 0)
	if err != nil {
		return agent.Execution{}, mapSaveError("create", err)
	}
	return withVersion(execution, cp.Version), nil
}

func (s *Store) Load(ctx context.Context, id string) (agent.Execution, error) {
	if err := s.validate(id); err != nil {
		return agent.Execution{}, err
	}
	cp, err := s.cp.Load(ctx, s.threadID(id))
	if err != nil {
		if errors.Is(err, checkpoint.ErrNotFound) {
			return agent.Execution{}, notFound(id)
		}
		return agent.Execution{}, fmt.Errorf("executionstore: load: %w", err)
	}
	if cp.Label != checkpointLabel || len(cp.Extra) == 0 {
		return agent.Execution{}, notFound(id)
	}
	execution, err := unmarshal(cp.Extra, id)
	if err != nil {
		return agent.Execution{}, err
	}
	execution.Usage = cp.Usage
	execution.Version = uint64(cp.Version)
	return execution, nil
}

func (s *Store) Save(ctx context.Context, execution agent.Execution, expectedVersion uint64) (agent.Execution, error) {
	if err := s.validate(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	if expectedVersion == 0 || expectedVersion > uint64(^uint(0)>>1) {
		return agent.Execution{}, fmt.Errorf("executionstore: save %q: %w", execution.ID, agent.ErrExecutionConflict)
	}
	data, err := marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	cp, err := s.cp.SaveIfVersion(ctx, s.threadID(execution.ID), checkpoint.Checkpoint{
		Label: checkpointLabel,
		Usage: execution.Usage,
		Extra: data,
	}, int(expectedVersion))
	if err != nil {
		return agent.Execution{}, mapSaveError("save", err)
	}
	return withVersion(execution, cp.Version), nil
}

func (s *Store) validate(id string) error {
	if s == nil || s.cp == nil {
		return errors.New("executionstore: checkpointer is required")
	}
	if id == "" {
		return errors.New("executionstore: execution ID is required")
	}
	return nil
}

func (s *Store) threadID(id string) string { return s.prefix + id }

func marshal(execution agent.Execution) ([]byte, error) {
	// Version is assigned by the Checkpointer, never trusted from the payload.
	execution.Version = 0
	data, err := json.Marshal(execution)
	if err != nil {
		return nil, fmt.Errorf("executionstore: marshal execution: %w", err)
	}
	return data, nil
}

func unmarshal(data []byte, wantID string) (agent.Execution, error) {
	var execution agent.Execution
	if err := json.Unmarshal(data, &execution); err != nil {
		return agent.Execution{}, fmt.Errorf("executionstore: unmarshal execution: %w", err)
	}
	if execution.ID != wantID {
		return agent.Execution{}, fmt.Errorf("executionstore: stored execution ID %q does not match requested ID %q", execution.ID, wantID)
	}
	return execution, nil
}

func withVersion(execution agent.Execution, version int) agent.Execution {
	execution.Version = uint64(version)
	return execution
}

func notFound(id string) error {
	return fmt.Errorf("executionstore: %q: %w", id, agent.ErrExecutionNotFound)
}

func mapSaveError(operation string, err error) error {
	if errors.Is(err, checkpoint.ErrConflict) || errors.Is(err, checkpoint.ErrNotFound) {
		return fmt.Errorf("executionstore: %s: %w", operation, errors.Join(agent.ErrExecutionConflict, err))
	}
	return fmt.Errorf("executionstore: %s: %w", operation, err)
}
