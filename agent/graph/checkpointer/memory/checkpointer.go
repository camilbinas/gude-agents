// Package memory provides an in-memory implementation of the GraphCheckpointer
// interface for testing and development use.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent/graph"
)

// Checkpointer is a thread-safe in-memory implementation of graph.GraphCheckpointer.
// It stores checkpoints in a map keyed by thread ID, with each thread maintaining
// an ordered slice of checkpoints by version.
type Checkpointer struct {
	mu      sync.RWMutex
	threads map[string][]graph.Checkpoint
}

// New creates a new in-memory Checkpointer.
func New() *Checkpointer {
	return &Checkpointer{threads: make(map[string][]graph.Checkpoint)}
}

// Save persists a checkpoint for the given thread. It assigns a sequential
// version starting from 1 and deep-copies the State and Completed maps.
func (c *Checkpointer) Save(_ context.Context, threadID string, cp graph.Checkpoint) (graph.Checkpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	existing := c.threads[threadID]
	version := len(existing) + 1

	saved := graph.Checkpoint{
		ThreadID:   threadID,
		Version:    version,
		State:      deepCopyState(cp.State),
		Completed:  deepCopyCompleted(cp.Completed),
		Iterations: cp.Iterations,
		Usage:      cp.Usage,
		NodeName:   cp.NodeName,
		Timestamp:  cp.Timestamp,
	}
	if saved.Timestamp.IsZero() {
		saved.Timestamp = time.Now()
	}

	c.threads[threadID] = append(existing, saved)
	return saved, nil
}

// Load returns the latest checkpoint (highest version) for the given thread.
// Returns graph.ErrCheckpointNotFound if no checkpoints exist.
func (c *Checkpointer) Load(_ context.Context, threadID string) (graph.Checkpoint, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cps, ok := c.threads[threadID]
	if !ok || len(cps) == 0 {
		return graph.Checkpoint{}, graph.ErrCheckpointNotFound
	}
	return deepCopyCheckpoint(cps[len(cps)-1]), nil
}

// LoadAt returns the checkpoint at a specific version for the given thread.
// Returns graph.ErrCheckpointNotFound if the version does not exist.
func (c *Checkpointer) LoadAt(_ context.Context, threadID string, version int) (graph.Checkpoint, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cps, ok := c.threads[threadID]
	if !ok {
		return graph.Checkpoint{}, graph.ErrCheckpointNotFound
	}
	for _, cp := range cps {
		if cp.Version == version {
			return deepCopyCheckpoint(cp), nil
		}
	}
	return graph.Checkpoint{}, graph.ErrCheckpointNotFound
}

// History returns checkpoint metadata ordered by version ascending for the given thread.
func (c *Checkpointer) History(_ context.Context, threadID string) ([]graph.CheckpointMeta, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cps := c.threads[threadID]
	metas := make([]graph.CheckpointMeta, len(cps))
	for i, cp := range cps {
		metas[i] = graph.CheckpointMeta{
			Version:   cp.Version,
			NodeName:  cp.NodeName,
			Timestamp: cp.Timestamp,
		}
	}
	return metas, nil
}

// List returns all thread IDs that have stored checkpoints.
func (c *Checkpointer) List(_ context.Context) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]string, 0, len(c.threads))
	for id := range c.threads {
		ids = append(ids, id)
	}
	return ids, nil
}

// Delete removes all checkpoints for the given thread.
func (c *Checkpointer) Delete(_ context.Context, threadID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.threads, threadID)
	return nil
}

// deepCopyState returns a shallow copy of the state map.
// Since State values are any (typically JSON-serializable primitives, slices, maps),
// a shallow copy at the top level prevents key-level mutation.
func deepCopyState(s graph.State) graph.State {
	if s == nil {
		return nil
	}
	out := make(graph.State, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// deepCopyCompleted returns a copy of the completed map.
func deepCopyCompleted(m map[string]bool) map[string]bool {
	if m == nil {
		return nil
	}
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// deepCopyCheckpoint returns a deep copy of a checkpoint with copied State and Completed maps.
func deepCopyCheckpoint(cp graph.Checkpoint) graph.Checkpoint {
	return graph.Checkpoint{
		ThreadID:   cp.ThreadID,
		Version:    cp.Version,
		State:      deepCopyState(cp.State),
		Completed:  deepCopyCompleted(cp.Completed),
		Iterations: cp.Iterations,
		Usage:      cp.Usage,
		NodeName:   cp.NodeName,
		Timestamp:  cp.Timestamp,
	}
}
