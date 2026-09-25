package checkpoint

import (
	"context"
	"sync"
	"time"
)

// Compile-time interface check.
var _ Checkpointer = (*MemoryCheckpointer)(nil)

// MemoryCheckpointer is a thread-safe in-memory Checkpointer intended for tests
// and development. Checkpoints are held per thread in version order and are lost
// when the process exits.
type MemoryCheckpointer struct {
	mu      sync.RWMutex
	threads map[string][]Checkpoint
}

// NewMemory creates an empty in-memory Checkpointer.
func NewMemory() *MemoryCheckpointer {
	return &MemoryCheckpointer{threads: make(map[string][]Checkpoint)}
}

// Save appends a checkpoint, assigning the next sequential version starting at 1.
func (c *MemoryCheckpointer) Save(_ context.Context, threadID string, cp Checkpoint) (Checkpoint, error) {
	if threadID == "" {
		return Checkpoint{}, ErrThreadIDRequired
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	existing := c.threads[threadID]

	saved := copyCheckpoint(cp)
	saved.ThreadID = threadID
	saved.Version = len(existing) + 1
	if saved.Timestamp.IsZero() {
		saved.Timestamp = time.Now()
	}

	c.threads[threadID] = append(existing, saved)
	return copyCheckpoint(saved), nil
}

// Load returns the highest-versioned checkpoint for the thread.
func (c *MemoryCheckpointer) Load(_ context.Context, threadID string) (Checkpoint, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cps := c.threads[threadID]
	if len(cps) == 0 {
		return Checkpoint{}, ErrNotFound
	}
	return copyCheckpoint(cps[len(cps)-1]), nil
}

// LoadAt returns the checkpoint at an exact version.
func (c *MemoryCheckpointer) LoadAt(_ context.Context, threadID string, version int) (Checkpoint, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, cp := range c.threads[threadID] {
		if cp.Version == version {
			return copyCheckpoint(cp), nil
		}
	}
	return Checkpoint{}, ErrNotFound
}

// History returns metadata for every checkpoint on the thread, oldest first.
func (c *MemoryCheckpointer) History(_ context.Context, threadID string) ([]Meta, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cps := c.threads[threadID]
	metas := make([]Meta, len(cps))
	for i, cp := range cps {
		metas[i] = Meta{Version: cp.Version, Label: cp.Label, Timestamp: cp.Timestamp}
	}
	return metas, nil
}

// List returns the thread IDs that have stored checkpoints.
func (c *MemoryCheckpointer) List(_ context.Context) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]string, 0, len(c.threads))
	for id := range c.threads {
		ids = append(ids, id)
	}
	return ids, nil
}

// Delete removes every checkpoint for the thread.
func (c *MemoryCheckpointer) Delete(_ context.Context, threadID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.threads, threadID)
	return nil
}

// copyCheckpoint isolates the stored checkpoint from the caller's maps and slices,
// so mutating either side after the call cannot affect the other.
func copyCheckpoint(cp Checkpoint) Checkpoint {
	out := cp
	out.State = CopyState(cp.State)
	if cp.Extra != nil {
		out.Extra = append([]byte(nil), cp.Extra...)
	}
	return out
}
