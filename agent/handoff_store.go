package agent

import "context"

// HandoffStore persists in-flight HandoffRequests so they survive process
// restarts and can be resumed from a different process (e.g. an HTTP server
// that stores pending handoffs in Redis instead of an in-memory map).
//
// Implementations must be safe for concurrent use.
//
// The key used for storage is always the ConversationID from the HandoffRequest.
// When ConversationID is empty the store must still accept the value; callers
// that rely on persistence should always populate ConversationID.
type HandoffStore interface {
	// SaveHandoff persists hr keyed by conversationID.
	// Overwrites any previously stored value for that key.
	SaveHandoff(ctx context.Context, conversationID string, hr *HandoffRequest) error

	// LoadHandoff retrieves the HandoffRequest for conversationID.
	// Returns nil, false (and a nil error) when no value is stored.
	LoadHandoff(ctx context.Context, conversationID string) (*HandoffRequest, bool, error)

	// DeleteHandoff removes the HandoffRequest for conversationID.
	// Returns nil if no value was stored.
	DeleteHandoff(ctx context.Context, conversationID string) error
}
