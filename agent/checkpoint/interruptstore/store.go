// Package interruptstore implements agent.InterruptStore on top of any
// checkpoint.Checkpointer, giving pending approvals and human-input requests
// durable resume across process restarts.
package interruptstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

var _ agent.InterruptStore = (*Store)(nil)

// DefaultThreadPrefix namespaces interrupts from other checkpoint consumers.
const DefaultThreadPrefix = "interrupt:"

const (
	checkpointLabel         = "interrupt"
	consumedCheckpointLabel = "interrupt-consumed"
)

// Store implements agent.InterruptStore backed by a Checkpointer.
type Store struct {
	cp     checkpoint.Checkpointer
	prefix string
}

// Option configures a Store.
type Option func(*Store)

// WithThreadPrefix overrides the prefix prepended to interrupt IDs.
func WithThreadPrefix(prefix string) Option { return func(s *Store) { s.prefix = prefix } }

// New wraps a Checkpointer as an agent.InterruptStore. A nil checkpointer is
// reported consistently by Save, Load, and Claim.
//
//	store := interruptstore.New(checkpoint.NewMemory())
//	a, _ := agent.New(prov, inst, agent.WithTools(tools...), agent.WithInterruptStore(store))
func New(cp checkpoint.Checkpointer, opts ...Option) *Store {
	s := &Store{cp: cp, prefix: DefaultThreadPrefix}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Store) validate() error {
	if s == nil || s.cp == nil {
		return fmt.Errorf("interruptstore: checkpointer is required")
	}
	return nil
}

// Save creates an interrupt checkpoint under in.ID.
func (s *Store) Save(ctx context.Context, in *agent.Interrupt) error {
	if err := s.validate(); err != nil {
		return err
	}
	if in == nil {
		return fmt.Errorf("interruptstore: interrupt is required")
	}
	if in.ID == "" {
		return fmt.Errorf("interruptstore: interrupt ID is required")
	}
	data, err := conversation.MarshalInterrupt(in)
	if err != nil {
		return fmt.Errorf("interruptstore: marshal interrupt: %w", err)
	}
	if _, err := s.cp.SaveIfVersion(ctx, s.threadID(in.ID), checkpoint.Checkpoint{Label: checkpointLabel, Extra: data}, 0); err != nil {
		return fmt.Errorf("interruptstore: save: %w", err)
	}
	return nil
}

// Load returns the pending interrupt stored under id.
func (s *Store) Load(ctx context.Context, id string) (*agent.Interrupt, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, fmt.Errorf("interruptstore: interrupt ID is required")
	}
	_, in, err := s.loadPending(ctx, id)
	return in, err
}

// Claim atomically consumes and returns the canonical pending interrupt.
func (s *Store) Claim(ctx context.Context, id string) (*agent.Interrupt, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, fmt.Errorf("interruptstore: interrupt ID is required")
	}
	cp, in, err := s.loadPending(ctx, id)
	if err != nil {
		return nil, err
	}
	_, err = s.cp.SaveIfVersion(ctx, s.threadID(id), checkpoint.Checkpoint{Label: consumedCheckpointLabel}, cp.Version)
	if err != nil {
		if errors.Is(err, checkpoint.ErrConflict) || errors.Is(err, checkpoint.ErrNotFound) {
			return nil, s.notFound(id)
		}
		return nil, fmt.Errorf("interruptstore: claim: %w", err)
	}
	return in, nil
}

func (s *Store) loadPending(ctx context.Context, id string) (checkpoint.Checkpoint, *agent.Interrupt, error) {
	cp, err := s.cp.Load(ctx, s.threadID(id))
	if err != nil {
		if errors.Is(err, checkpoint.ErrNotFound) {
			return checkpoint.Checkpoint{}, nil, s.notFound(id)
		}
		return checkpoint.Checkpoint{}, nil, fmt.Errorf("interruptstore: load: %w", err)
	}
	if cp.Label != checkpointLabel || len(cp.Extra) == 0 {
		return checkpoint.Checkpoint{}, nil, s.notFound(id)
	}
	in, err := conversation.UnmarshalInterrupt(cp.Extra)
	if err != nil {
		return checkpoint.Checkpoint{}, nil, fmt.Errorf("interruptstore: unmarshal interrupt: %w", err)
	}
	if in.ID != id {
		return checkpoint.Checkpoint{}, nil, fmt.Errorf("interruptstore: stored interrupt ID %q does not match requested ID %q", in.ID, id)
	}
	return cp, in, nil
}

func (s *Store) notFound(id string) error {
	return fmt.Errorf("interruptstore: %q: %w", id, agent.ErrInterruptNotFound)
}

func (s *Store) threadID(id string) string { return s.prefix + id }
