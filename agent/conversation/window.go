package conversation

import (
	"context"

	"github.com/camilbinas/gude-agents/agent"
)

// compile-time check
var _ agent.Conversation = (*Window)(nil)

// Window wraps a Conversation and returns only the last N messages on Load.
type Window struct {
	inner agent.Conversation
	n     int
}

// NewWindow creates a Window that retains the last n messages on Load.
func NewWindow(inner agent.Conversation, n int) *Window {
	return &Window{inner: inner, n: n}
}

// Load retrieves messages from the inner store and returns only the last n,
// adjusted forward to a safe boundary so that no tool_result block in the
// returned slice references a tool_use block that was truncated away.
func (w *Window) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	msgs, err := w.inner.Load(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if start := len(msgs) - w.n; start > 0 {
		msgs = safeTruncate(msgs, start)
	}
	return msgs, nil
}

// Save delegates directly to the inner store without modification.
func (w *Window) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	return w.inner.Save(ctx, conversationID, messages)
}

// safeTruncate returns msgs[start:] adjusted forward so that no tool_result
// in the slice references a tool_use that was cut off before start.
func safeTruncate(msgs []agent.Message, start int) []agent.Message {
	for start < len(msgs) {
		// Collect all tool_use IDs present in msgs[start:].
		present := make(map[string]bool)
		for _, m := range msgs[start:] {
			for _, b := range m.Content {
				if tu, ok := b.(agent.ToolUseBlock); ok {
					present[tu.ToolUseID] = true
				}
			}
		}
		// Check whether any tool_result in msgs[start:] is orphaned.
		orphaned := false
		for _, m := range msgs[start:] {
			for _, b := range m.Content {
				if tr, ok := b.(agent.ToolResultBlock); ok {
					if !present[tr.ToolUseID] {
						orphaned = true
						break
					}
				}
			}
			if orphaned {
				break
			}
		}
		if !orphaned {
			break
		}
		// Advance past the current message and try again.
		start++
	}
	if start >= len(msgs) {
		return nil
	}
	return msgs[start:]
}
