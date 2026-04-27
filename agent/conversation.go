package agent

import "context"

// Conversation persists conversation history across invocations.
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

// conversationIDKey is the context key for per-invocation conversation ID override.
type conversationIDKey struct{}

// WithConversationID returns a context that overrides the agent's default
// conversationID for this invocation. This allows a single Agent instance
// to serve multiple concurrent conversations in HTTP environments.
//
//	ctx := agent.WithConversationID(ctx, "user-123-session-456")
//	result, _, err := a.Invoke(ctx, "hello")
func WithConversationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, conversationIDKey{}, id)
}

// ResolveConversationID returns the per-invocation override if set,
// otherwise falls back to the provided default.
func ResolveConversationID(ctx context.Context, fallback string) string {
	if id, ok := ctx.Value(conversationIDKey{}).(string); ok && id != "" {
		return id
	}
	return fallback
}
