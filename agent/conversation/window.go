package conversation

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

var _ agent.ConversationStore = (*Window)(nil)

// Window wraps a ConversationStore and returns only the last N messages on Load.
type Window struct {
	inner agent.ConversationStore
	n     int
}

func NewWindow(inner agent.ConversationStore, n int) *Window {
	if n < 1 {
		panic("conversation: window size must be >= 1")
	}
	return &Window{inner: inner, n: n}
}

func (w *Window) Load(ctx context.Context, conversationID string) (agent.ConversationSnapshot, error) {
	snapshot, err := w.inner.Load(ctx, conversationID)
	if err != nil {
		return agent.ConversationSnapshot{}, err
	}
	if start := len(snapshot.Messages) - w.n; start > 0 {
		snapshot.Messages = safeTruncate(snapshot.Messages, start)
	}
	return snapshot, nil
}

func (w *Window) Save(ctx context.Context, conversationID string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	return w.inner.Save(ctx, conversationID, messages, expectedRevision)
}

func safeTruncate(msgs []agent.Message, start int) []agent.Message {
	for start < len(msgs) {
		present := make(map[string]bool)
		for _, m := range msgs[start:] {
			for _, b := range m.Content {
				if tu, ok := b.(agent.ToolUseBlock); ok {
					present[tu.ToolUseID] = true
				}
			}
		}
		orphaned := false
		for _, m := range msgs[start:] {
			for _, b := range m.Content {
				if tr, ok := b.(agent.ToolResultBlock); ok && !present[tr.ToolUseID] {
					orphaned = true
					break
				}
			}
			if orphaned {
				break
			}
		}
		if !orphaned {
			break
		}
		start++
	}
	if start >= len(msgs) {
		return nil
	}
	return msgs[start:]
}

func (w *Window) List(ctx context.Context) ([]string, error) {
	manager, ok := w.inner.(agent.ConversationManager)
	if !ok {
		return nil, fmt.Errorf("conversation: inner store does not support List")
	}
	return manager.List(ctx)
}

func (w *Window) Delete(ctx context.Context, conversationID string) error {
	manager, ok := w.inner.(agent.ConversationManager)
	if !ok {
		return fmt.Errorf("conversation: inner store does not support Delete")
	}
	return manager.Delete(ctx, conversationID)
}

func (w *Window) Flush(ctx context.Context) error {
	if flusher, ok := w.inner.(agent.Flusher); ok {
		return flusher.Flush(ctx)
	}
	return nil
}
