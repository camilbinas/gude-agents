package graph

import "context"

// GraphTracingHook is an optional interface for tracing graph execution.
// The tracing submodule provides the concrete implementation.
// The graph calls these methods at key lifecycle points when the hook is non-nil.
type GraphTracingHook interface {
	// OnGraphRunStart is called at the beginning of Graph.Run.
	// Returns a context with the root span and a finish function.
	OnGraphRunStart(ctx context.Context) (context.Context, func(err error, iterations int))

	// OnNodeStart is called before each node execution.
	// Returns a context with the node span and a finish function.
	OnNodeStart(ctx context.Context, nodeName string) (context.Context, func(err error))

	// OnCheckpointSave is called when a checkpoint is saved.
	// Returns a finish function called after the save completes.
	OnCheckpointSave(ctx context.Context, nodeName string, version int) func(err error)

	// OnInterrupt is called when an interrupt fires.
	OnInterrupt(ctx context.Context, nodeName string, interruptType InterruptType, version int)

	// OnResume is called when execution resumes from a checkpoint.
	OnResume(ctx context.Context, threadID string, version int)

	// OnRewind is called when execution rewinds to a previous version.
	OnRewind(ctx context.Context, threadID string, targetVersion int)
}
