package conversation

import (
	"context"

	"github.com/camilbinas/gude-agents/agent"
)

// ConversationManager extends Conversation with conversation lifecycle operations.
type ConversationManager interface {
	agent.Conversation
	List(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, conversationID string) error
}

// Compile-time check that Store implements ConversationManager.
var _ ConversationManager = (*InMemory)(nil)
