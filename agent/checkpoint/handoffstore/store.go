// Package handoffstore implements agent.HandoffStore on top of any
// checkpoint.Checkpointer, giving paused agents durable resume across process
// restarts.
//
// A handoff is a paused agent waiting on human input, which is the same shape as
// a checkpoint: snapshot now, resume later. Each conversation maps to a checkpoint
// thread, and the serialized HandoffRequest travels in the checkpoint's opaque
// Extra field.
//
// The store lives here rather than in the core agent package because
// agent/checkpoint depends on core — implementing the interface from this side
// avoids a module cycle, the same arrangement the conversation backends use.
//
// # Usage
//
//	store := handoffstore.New(checkpoint.NewMemory())
//	agent.New(prov, inst, tools, agent.WithHandoffStore(store))
package handoffstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

// Compile-time interface check.
var _ agent.HandoffStore = (*Store)(nil)

// DefaultThreadPrefix namespaces handoff threads so they cannot collide with
// other checkpoint consumers sharing the same backing table.
const DefaultThreadPrefix = "handoff:"

// checkpointLabel marks handoff checkpoints in History listings.
const checkpointLabel = "handoff"

// Store implements agent.HandoffStore backed by a checkpoint.Checkpointer.
type Store struct {
	cp     checkpoint.Checkpointer
	prefix string
}

// Option configures a Store.
type Option func(*Store)

// WithThreadPrefix overrides the prefix prepended to conversation IDs when
// forming checkpoint thread IDs. Default: DefaultThreadPrefix.
func WithThreadPrefix(prefix string) Option {
	return func(s *Store) { s.prefix = prefix }
}

// New wraps a Checkpointer as an agent.HandoffStore.
func New(cp checkpoint.Checkpointer, opts ...Option) *Store {
	s := &Store{cp: cp, prefix: DefaultThreadPrefix}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SaveHandoff persists an in-flight handoff for the conversation.
//
// Saves append rather than overwrite, since the underlying Checkpointer is
// append-only. LoadHandoff always returns the most recent, and earlier versions
// remain available through the Checkpointer's History and LoadAt as an audit
// trail. DeleteHandoff discards the whole thread.
func (s *Store) SaveHandoff(ctx context.Context, conversationID string, hr *agent.HandoffRequest) error {
	if conversationID == "" {
		return fmt.Errorf("handoffstore: conversation ID is required")
	}
	if hr == nil {
		return fmt.Errorf("handoffstore: handoff request is required")
	}

	// Uses the same type-discriminated encoding as the durable conversation
	// stores, so ContentBlocks survive a round trip through another process.
	data, err := conversation.MarshalHandoffRequest(hr)
	if err != nil {
		return fmt.Errorf("handoffstore: marshal handoff: %w", err)
	}

	if _, err := s.cp.Save(ctx, s.threadID(conversationID), checkpoint.Checkpoint{
		Label: checkpointLabel,
		Extra: data,
	}); err != nil {
		return fmt.Errorf("handoffstore: save: %w", err)
	}
	return nil
}

// LoadHandoff returns the most recent handoff for the conversation.
// Returns nil, false, nil when nothing is stored.
func (s *Store) LoadHandoff(ctx context.Context, conversationID string) (*agent.HandoffRequest, bool, error) {
	cp, err := s.cp.Load(ctx, s.threadID(conversationID))
	if err != nil {
		// A missing thread is an ordinary "not stored" result, not a failure.
		if errors.Is(err, checkpoint.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("handoffstore: load: %w", err)
	}

	if len(cp.Extra) == 0 {
		return nil, false, nil
	}

	hr, err := conversation.UnmarshalHandoffRequest(cp.Extra)
	if err != nil {
		return nil, false, fmt.Errorf("handoffstore: unmarshal handoff: %w", err)
	}
	return hr, true, nil
}

// DeleteHandoff removes every stored handoff for the conversation.
// Deleting a conversation with no handoff is not an error.
func (s *Store) DeleteHandoff(ctx context.Context, conversationID string) error {
	if err := s.cp.Delete(ctx, s.threadID(conversationID)); err != nil {
		return fmt.Errorf("handoffstore: delete: %w", err)
	}
	return nil
}

// threadID maps a conversation ID onto its checkpoint thread.
func (s *Store) threadID(conversationID string) string {
	return s.prefix + conversationID
}
