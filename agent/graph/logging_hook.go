package graph

import (
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// GraphLoggingHook is an optional interface for structured logging of graph execution.
// The logging submodule provides the concrete implementation.
// The graph calls these methods at key lifecycle points when the hook is non-nil.
type GraphLoggingHook interface {
	// OnGraphRunStart is called at the beginning of Graph.Run.
	OnGraphRunStart()

	// OnGraphRunEnd is called at the end of Graph.Run with the outcome.
	OnGraphRunEnd(err error, iterations int, usage agent.TokenUsage, duration time.Duration)

	// OnNodeStart is called before each node execution.
	OnNodeStart(nodeName string)

	// OnNodeEnd is called after each node execution with the outcome.
	OnNodeEnd(nodeName string, err error, duration time.Duration)
}

// SetGraphLoggingHook sets the logging hook on the graph.
// Called by the logging submodule's GraphOption.
func (g *Graph) SetGraphLoggingHook(h GraphLoggingHook) {
	g.loggingHook = h
}

// GraphLoggingHook returns the graph's logging hook, or nil if none is set.
func (g *Graph) GraphLoggingHook() GraphLoggingHook {
	return g.loggingHook
}
