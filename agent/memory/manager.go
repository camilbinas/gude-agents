package memory

import (
	"context"

	"github.com/camilbinas/gude-agents/agent"
)

// MemoryManager extends Memory with conversation lifecycle operations.
type MemoryManager interface {
	agent.Memory
	List(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, conversationID string) error
}

// Compile-time check that Store implements MemoryManager.
var _ MemoryManager = (*Store)(nil)
