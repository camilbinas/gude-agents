package graph

// RunOption configures a single Graph.Run call.
type RunOption func(*runConfig)

// runConfig holds per-Run configuration parsed from RunOptions.
type runConfig struct {
	threadID string
	// extraEventHook is an additional GraphEventHook applied for the duration
	// of a single Run call, on top of any hook configured via WithEventHook /
	// SetEventHook on the graph itself. Used internally by RunEventStream to
	// fan events into a per-call channel without mutating shared graph state.
	// Nil means no extra hook.
	extraEventHook GraphEventHook
}

// WithThreadID sets the thread ID for checkpointing during a Run call.
// When a checkpointer is configured on the graph, a thread ID is required.
// When no checkpointer is configured, the thread ID is ignored.
func WithThreadID(id string) RunOption {
	return func(c *runConfig) {
		c.threadID = id
	}
}
