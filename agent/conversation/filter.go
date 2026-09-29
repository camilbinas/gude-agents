package conversation

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

var _ agent.ConversationStore = (*Filter)(nil)

// Filter wraps a ConversationStore and strips non-text blocks on Load.
type Filter struct{ inner agent.ConversationStore }

func NewFilter(inner agent.ConversationStore) *Filter { return &Filter{inner: inner} }

func (f *Filter) Load(ctx context.Context, conversationID string) (agent.ConversationSnapshot, error) {
	snapshot, err := f.inner.Load(ctx, conversationID)
	if err != nil {
		return agent.ConversationSnapshot{}, err
	}
	filtered := make([]agent.Message, 0, len(snapshot.Messages))
	for _, msg := range snapshot.Messages {
		var textBlocks []agent.ContentBlock
		for _, block := range msg.Content {
			if _, ok := block.(agent.TextBlock); ok {
				textBlocks = append(textBlocks, block)
			}
		}
		if len(textBlocks) > 0 {
			filtered = append(filtered, agent.Message{Role: msg.Role, Content: textBlocks})
		}
	}
	snapshot.Messages = filtered
	return snapshot, nil
}

func (f *Filter) Save(ctx context.Context, conversationID string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	return f.inner.Save(ctx, conversationID, messages, expectedRevision)
}

func (f *Filter) List(ctx context.Context) ([]string, error) {
	manager, ok := f.inner.(agent.ConversationManager)
	if !ok {
		return nil, fmt.Errorf("conversation: inner store does not support List")
	}
	return manager.List(ctx)
}

func (f *Filter) Delete(ctx context.Context, conversationID string) error {
	manager, ok := f.inner.(agent.ConversationManager)
	if !ok {
		return fmt.Errorf("conversation: inner store does not support Delete")
	}
	return manager.Delete(ctx, conversationID)
}

func (f *Filter) Flush(ctx context.Context) error {
	if flusher, ok := f.inner.(agent.Flusher); ok {
		return flusher.Flush(ctx)
	}
	return nil
}
