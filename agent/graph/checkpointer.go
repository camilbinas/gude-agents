package graph

import (
	"context"
	"errors"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// Sentinel errors for checkpoint operations.
var (
	// ErrCheckpointNotFound is returned when a Load or LoadAt references
	// a thread or version that does not exist.
	ErrCheckpointNotFound = errors.New("graph: checkpoint not found")

	// ErrNoCheckpointer is returned when Step/Resume/RewindTo is called
	// without a configured checkpointer.
	ErrNoCheckpointer = errors.New("graph: no checkpointer configured")

	// ErrThreadIDRequired is returned when a checkpointer is configured
	// but no thread ID is provided.
	ErrThreadIDRequired = errors.New("graph: thread ID is required when checkpointer is configured")
)

// GraphCheckpointer persists and retrieves execution checkpoints.
// Implementations must be safe for concurrent use.
type GraphCheckpointer interface {
	// Save persists a checkpoint for the given thread.
	// The checkpointer assigns the Version field before storing.
	Save(ctx context.Context, threadID string, cp Checkpoint) (Checkpoint, error)

	// Load retrieves the latest checkpoint for the given thread.
	// Returns ErrCheckpointNotFound if no checkpoints exist.
	Load(ctx context.Context, threadID string) (Checkpoint, error)

	// LoadAt retrieves the checkpoint at a specific version.
	// Returns ErrCheckpointNotFound if the version does not exist.
	LoadAt(ctx context.Context, threadID string, version int) (Checkpoint, error)

	// History returns ordered checkpoint metadata for a thread (oldest first).
	History(ctx context.Context, threadID string) ([]CheckpointMeta, error)

	// List returns all thread IDs that have stored checkpoints.
	List(ctx context.Context) ([]string, error)

	// Delete removes all checkpoints for a thread.
	Delete(ctx context.Context, threadID string) error
}

// Checkpoint captures the full execution context at a point in time.
type Checkpoint struct {
	ThreadID     string           `json:"thread_id"`
	Version      int              `json:"version"`
	State        State            `json:"state"`
	Completed    map[string]bool  `json:"completed"`
	ReadinessSet map[string]bool  `json:"readiness_set,omitempty"`
	Iterations   int              `json:"iterations"`
	Usage        agent.TokenUsage `json:"usage"`
	NodeName     string           `json:"node_name"`
	Timestamp    time.Time        `json:"timestamp"`
}

// CheckpointMeta is lightweight metadata returned by History.
type CheckpointMeta struct {
	Version   int       `json:"version"`
	NodeName  string    `json:"node_name"`
	Timestamp time.Time `json:"timestamp"`
}
