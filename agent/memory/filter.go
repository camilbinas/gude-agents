package memory

import (
	"context"

	"github.com/camilbinas/gude-agents/agent"
)

// compile-time check
var _ agent.Memory = (*Filter)(nil)

// Filter wraps a Memory and strips ToolUseBlock and ToolResultBlock
// from messages on Load, returning only TextBlock content.
// Documented in docs/memory.md — update when changing behavior.
type Filter struct {
	inner agent.Memory
}

// NewFilter creates a Filter that retains only TextBlock content on Load.
func NewFilter(inner agent.Memory) *Filter {
	return &Filter{inner: inner}
}

// Load retrieves messages from the inner store and removes all non-TextBlock
// content blocks. Messages with no remaining content blocks are omitted.
func (f *Filter) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	msgs, err := f.inner.Load(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	var filtered []agent.Message
	for _, msg := range msgs {
		var textBlocks []agent.ContentBlock
		for _, b := range msg.Content {
			switch b.(type) {
			case agent.TextBlock:
				textBlocks = append(textBlocks, b)
			}
		}
		if len(textBlocks) > 0 {
			filtered = append(filtered, agent.Message{
				Role:    msg.Role,
				Content: textBlocks,
			})
		}
	}
	return filtered, nil
}

// Save delegates directly to the inner store without modification.
func (f *Filter) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	return f.inner.Save(ctx, conversationID, messages)
}
