package graph

// RunOption configures a single Graph.Run call.
type RunOption func(*runConfig)

// runConfig holds per-Run configuration parsed from RunOptions.
type runConfig struct {
	threadID string
}

// WithThreadID sets the thread ID for checkpointing during a Run call.
// When a checkpointer is configured on the graph, a thread ID is required.
// When no checkpointer is configured, the thread ID is ignored.
func WithThreadID(id string) RunOption {
	return func(c *runConfig) {
		c.threadID = id
	}
}
