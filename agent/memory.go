package agent

import "context"

// Memory persists conversation history across invocations.
// Documented in docs/memory.md — update when changing interface methods.
type Memory interface {
	// Load retrieves messages for the given conversation ID.
	Load(ctx context.Context, conversationID string) ([]Message, error)

	// Save persists messages for the given conversation ID.
	Save(ctx context.Context, conversationID string, messages []Message) error
}

// MemoryWaiter is an optional interface that Memory implementations can satisfy
// to signal that they perform background work after Save. When the agent option
// WithSynchronousMemory is set, the agent calls Wait after each Save, blocking
// until all background work (e.g. summarization) is complete.
type MemoryWaiter interface {
	Wait()
}
