package agent

import "time"

// LoggingHook is an optional interface for structured logging instrumentation.
// The logging submodule provides the concrete implementation.
// The agent calls these methods at key lifecycle points when the hook is non-nil.
type LoggingHook interface {
	// OnInvokeStart is called at the beginning of InvokeStream/Invoke.
	OnInvokeStart(params InvokeSpanParams)

	// OnInvokeEnd is called at the end of InvokeStream/Invoke with the outcome.
	OnInvokeEnd(err error, usage TokenUsage, duration time.Duration)

	// OnIterationStart is called at the beginning of each agent loop iteration.
	OnIterationStart(iteration int)

	// OnProviderCallStart is called before each Provider.ConverseStream call.
	// modelID is from ModelIdentifier (empty if not implemented).
	OnProviderCallStart(modelID string)

	// OnProviderCallEnd is called after each Provider.ConverseStream call with the outcome.
	OnProviderCallEnd(err error, usage TokenUsage, toolCallCount int, duration time.Duration)

	// OnToolStart is called before each tool execution.
	OnToolStart(toolName string)

	// OnToolEnd is called after each tool execution with the outcome.
	OnToolEnd(toolName string, err error, duration time.Duration)

	// OnGuardrailComplete is called after a guardrail evaluation.
	// direction is "input" or "output". blocked is true if the guardrail rejected.
	OnGuardrailComplete(direction string, blocked bool, err error)

	// OnMemoryStart is called before memory Load or Save.
	OnMemoryStart(operation string, conversationID string)

	// OnMemoryEnd is called after memory Load or Save with the outcome.
	OnMemoryEnd(operation string, conversationID string, err error, messageCount int, duration time.Duration)

	// OnRetrieverStart is called before Retriever.Retrieve.
	OnRetrieverStart(query string)

	// OnRetrieverEnd is called after Retriever.Retrieve with the outcome.
	OnRetrieverEnd(err error, docCount int, duration time.Duration)

	// OnMaxIterationsExceeded records the max-iterations-exceeded event.
	OnMaxIterationsExceeded(limit int)
}

// SetLoggingHook sets the logging hook. Called by the logging submodule's Option.
func (a *Agent) SetLoggingHook(h LoggingHook) {
	a.loggingHook = h
}

// LoggingHook returns the agent's logging hook, or nil if none is set.
func (a *Agent) LoggingHook() LoggingHook {
	return a.loggingHook
}
