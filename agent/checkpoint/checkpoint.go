// Package checkpoint provides a durable, versioned, thread-scoped state store
// with history and point-in-time recovery.
//
// A Checkpointer is a general-purpose primitive for any resumable workload: it
// records successive snapshots of a State map under a thread ID, assigns each a
// monotonically increasing version, and allows loading either the latest snapshot
// or any earlier version.
//
// Consumers that need to persist their own bookkeeping alongside State put it in
// the Extra field as pre-marshalled JSON. Backends treat Extra as an opaque JSON
// value and preserve its meaning without interpreting consumer-owned fields.
package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// Sentinel errors returned by Checkpointer implementations.
var (
	// ErrNotFound is returned by Load and LoadAt when the thread or version
	// does not exist.
	ErrNotFound = errors.New("checkpoint: not found")

	// ErrThreadIDRequired is returned when an empty thread ID is supplied.
	ErrThreadIDRequired = errors.New("checkpoint: thread ID is required")
)

// State is the caller-owned snapshot payload. It is stored as queryable JSON,
// so backends that support structured columns (such as Postgres JSONB) can index
// and query into it.
type State = map[string]any

// Checkpoint is a single versioned snapshot of a thread's state.
type Checkpoint struct {
	// ThreadID identifies the logical execution thread. Assigned by the
	// Checkpointer on Save.
	ThreadID string `json:"thread_id"`

	// Version is a monotonically increasing sequence number starting at 1.
	// Assigned by the Checkpointer on Save.
	Version int `json:"version"`

	// Label is an optional short marker describing what produced this
	// checkpoint, surfaced in History without loading full snapshots.
	Label string `json:"label,omitempty"`

	// State is the snapshot payload.
	State State `json:"state"`

	// Usage carries token accounting accumulated up to this checkpoint.
	Usage agent.TokenUsage `json:"usage"`

	// Timestamp is when the checkpoint was taken. Defaulted to the current
	// time by the Checkpointer if zero.
	Timestamp time.Time `json:"timestamp"`

	// Extra holds consumer-owned bookkeeping as pre-marshalled JSON. Backends
	// preserve its JSON meaning without interpreting consumer-owned fields.
	Extra json.RawMessage `json:"extra,omitempty"`
}

// Meta is lightweight checkpoint metadata, returned by History so callers can
// inspect a thread's timeline without deserializing every snapshot.
type Meta struct {
	Version   int       `json:"version"`
	Label     string    `json:"label,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Checkpointer persists and retrieves versioned state snapshots.
// Implementations must be safe for concurrent use.
type Checkpointer interface {
	// Save appends a checkpoint for the given thread, assigning the next
	// version and returning the stored result.
	Save(ctx context.Context, threadID string, cp Checkpoint) (Checkpoint, error)

	// Load returns the highest-versioned checkpoint for the thread, or
	// ErrNotFound if the thread has none.
	Load(ctx context.Context, threadID string) (Checkpoint, error)

	// LoadAt returns the checkpoint at an exact version, or ErrNotFound.
	LoadAt(ctx context.Context, threadID string, version int) (Checkpoint, error)

	// History returns metadata for every checkpoint on the thread, oldest first.
	History(ctx context.Context, threadID string) ([]Meta, error)

	// List returns the thread IDs that have stored checkpoints.
	//
	// This is an unbounded enumeration. Backend cost varies: implementations
	// may scan storage or maintain an index. Treat it as an administrative or
	// development operation rather than something to call on a request path.
	List(ctx context.Context) ([]string, error)

	// Delete removes every checkpoint for the thread. Deleting a thread that
	// does not exist is not an error.
	Delete(ctx context.Context, threadID string) error
}

// CopyState returns a copy of s, guarding stored snapshots against later mutation
// of the caller's map. Values are copied by reference; callers holding mutable
// values inside State must not mutate them after Save.
func CopyState(s State) State {
	if s == nil {
		return nil
	}
	out := make(State, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}
