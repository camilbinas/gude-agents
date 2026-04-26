package agent

import "context"

// Conversation persists conversation history across invocations.
// Documented in docs/conversation.md — update when changing interface methods.
type Conversation interface {
	// Load retrieves messages for the given conversation ID.
	Load(ctx context.Context, conversationID string) ([]Message, error)

	// Save persists messages for the given conversation ID.
	Save(ctx context.Context, conversationID string, messages []Message) error
}

// ConversationWaiter is an optional interface that Conversation implementations
// can satisfy to signal that they perform background work after Save. When the
// agent option WithSynchronousConversation is set, the agent calls Wait after
// each Save, blocking until all background work (e.g. summarization) is complete.
type ConversationWaiter interface {
	Wait()
}
