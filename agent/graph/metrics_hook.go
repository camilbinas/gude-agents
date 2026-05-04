package graph

// GraphMetricsHook is an optional interface for metrics instrumentation of graph execution.
// The metrics submodule provides the concrete implementation.
// The graph calls these methods at key lifecycle points when the hook is non-nil.
type GraphMetricsHook interface {
	// OnGraphRunStart is called at the beginning of Graph.Run.
	// Returns a finish function called with the outcome and iteration count.
	OnGraphRunStart() func(err error, iterations int)

	// OnNodeStart is called before each node execution.
	// Returns a finish function called with the outcome.
	OnNodeStart(nodeName string) func(err error)
}
