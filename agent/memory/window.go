package memory

import (
	"context"

	"github.com/camilbinas/gude-agents/agent"
)

// compile-time check
var _ agent.Memory = (*Window)(nil)

// Window wraps a Memory and returns only the last N messages on Load.
// Documented in docs/memory.md — update when changing behavior.
type Window struct {
	inner agent.Memory
	n     int
}

// NewWindow creates a Window that retains the last n messages on Load.
func NewWindow(inner agent.Memory, n int) *Window {
	return &Window{inner: inner, n: n}
}

// Load retrieves messages from the inner store and returns only the last n.
func (w *Window) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	msgs, err := w.inner.Load(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if start := len(msgs) - w.n; start > 0 {
		msgs = msgs[start:]
	}
	return msgs, nil
}

// Save delegates directly to the inner store without modification.
func (w *Window) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	return w.inner.Save(ctx, conversationID, messages)
}
