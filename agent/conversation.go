package agent

import "context"

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
