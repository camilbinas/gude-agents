package agent

// MetricsHook is an optional interface for metrics instrumentation.
// The metrics submodule provides the concrete implementation.
// The agent calls these methods at key lifecycle points when the hook is non-nil.
type MetricsHook interface {
	// OnInvokeStart is called at the beginning of InvokeStream/Invoke.
	// Returns a finish function called with the outcome and token usage.
	OnInvokeStart() func(err error, usage TokenUsage)

	// OnIterationStart is called at the beginning of each agent loop iteration.
	OnIterationStart()

	// OnProviderCallStart is called before each Provider.ConverseStream call.
	// modelID is from ModelIdentifier (empty if not implemented).
	// Returns a finish function called with the outcome and token usage.
	OnProviderCallStart(modelID string) func(err error, usage TokenUsage)

	// OnToolStart is called before each tool execution.
	// Returns a finish function called with the outcome.
	OnToolStart(toolName string) func(err error)

	// OnGuardrailComplete is called after a guardrail evaluation.
	// direction is "input" or "output". blocked is true if the guardrail rejected.
	OnGuardrailComplete(direction string, blocked bool)

	// OnImagesAttached is called when images are attached to the invocation via WithImages.
	// imageCount is the number of ImageBlock values prepended to the first user message.
	OnImagesAttached(imageCount int)
}

// SetMetricsHook sets the metrics hook. Called by the metrics submodule's Option.
func (a *Agent) SetMetricsHook(h MetricsHook) {
	a.metricsHook = h
}

// MetricsHook returns the agent's metrics hook, or nil if none is set.
func (a *Agent) MetricsHook() MetricsHook {
	return a.metricsHook
}
