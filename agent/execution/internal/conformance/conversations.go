package conformance

import (
	"context"
	"fmt"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// Conversations is a minimal in-memory agent.ConversationStore for backend
// pause/resume tests. It exists so the execution backend modules do not import
// a conversation backend (and its dependencies) just to test execution
// persistence. It follows the ConversationStore contract: contiguous sequences
// from 1, one revision per successful Append, and a revision CAS.
type Conversations struct {
	mu    sync.Mutex
	items map[string][]agent.Message
	revs  map[string]uint64
}

var _ agent.ConversationStore = (*Conversations)(nil)

// NewConversations returns an empty store.
func NewConversations() *Conversations {
	return &Conversations{items: make(map[string][]agent.Message), revs: make(map[string]uint64)}
}

// Load returns the whole transcript.
func (c *Conversations) Load(ctx context.Context, id string) (agent.ConversationSnapshot, error) {
	return c.LoadAfter(ctx, id, 0)
}

// LoadAfter returns messages with sequence greater than after.
func (c *Conversations) LoadAfter(_ context.Context, id string, after uint64) (agent.ConversationSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	all := c.items[id]
	if after > uint64(len(all)) {
		after = uint64(len(all))
	}
	return agent.ConversationSnapshot{
		Messages:     append([]agent.Message(nil), all[after:]...),
		Revision:     c.revs[id],
		LastSequence: uint64(len(all)),
	}, nil
}

// Append appends the batch when expectedRevision is current.
func (c *Conversations) Append(_ context.Context, id string, messages []agent.Message, expectedRevision uint64) (agent.ConversationCursor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.revs[id] != expectedRevision {
		return agent.ConversationCursor{}, fmt.Errorf("conversation %q: %w", id, agent.ErrConversationConflict)
	}
	if len(messages) > 0 {
		c.items[id] = append(c.items[id], messages...)
		c.revs[id]++
	}
	return agent.ConversationCursor{Revision: c.revs[id], LastSequence: uint64(len(c.items[id]))}, nil
}
