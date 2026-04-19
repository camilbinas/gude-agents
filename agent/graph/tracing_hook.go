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
}

// SetGraphTracingHook sets the tracing hook on the graph.
// Called by the tracing submodule's GraphOption.
func (g *Graph) SetGraphTracingHook(h GraphTracingHook) {
	g.tracingHook = h
}
