package execution

import "github.com/camilbinas/gude-agents/agent/checkpoint"

// NewMemory returns an ExecutionStore backed by an in-process memory
// checkpointer.
//
// It is intended for tests, development, and examples. Paused executions and
// all other execution state live only in the running process: they are lost
// when the process exits, are not shared between processes, and are never
// evicted. Use New with a durable checkpoint.Checkpointer (PostgreSQL, Redis,
// DynamoDB) when a pause must survive a restart.
//
// A paused run also needs a ConversationStore that owns its transcript; pair
// NewMemory with a conversation store whose durability matches your needs.
func NewMemory(opts ...Option) *Store {
	return New(checkpoint.NewMemory(), opts...)
}
