package agent

import "context"

// HandoffStore persists in-flight HandoffRequests across process restarts.
// Implementations must be safe for concurrent use.
type HandoffStore interface {
	SaveHandoff(ctx context.Context, conversationID string, hr *HandoffRequest) error
	// LoadHandoff returns nil, false, nil when no value is stored.
	LoadHandoff(ctx context.Context, conversationID string) (*HandoffRequest, bool, error)
	DeleteHandoff(ctx context.Context, conversationID string) error
}
