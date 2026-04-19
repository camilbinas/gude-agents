package agent

import (
	"context"
	"encoding/json"
)

// TracingHook is an optional interface for tracing instrumentation.
// The tracing submodule provides the concrete implementation.
// The agent calls these methods at key lifecycle points when the hook is non-nil.
type TracingHook interface {
	// OnInvokeStart is called at the beginning of InvokeStream/Invoke.
	// Returns a context with the root span and a finish function.
	OnInvokeStart(ctx context.Context, params InvokeSpanParams) (context.Context, func(err error, usage TokenUsage, response string))

	// OnIterationStart is called at the beginning of each agent loop iteration.
	OnIterationStart(ctx context.Context, iteration int) (context.Context, func(toolCount int, isFinal bool))

	// OnProviderCallStart is called before each Provider.ConverseStream call.
	// system is the system prompt, messages is the conversation sent to the provider.
	OnProviderCallStart(ctx context.Context, params ProviderCallParams) (context.Context, func(err error, usage TokenUsage, toolCallCount int, responseText string))

	// OnToolStart is called before each tool execution.
	// input is the raw JSON input to the tool.
	OnToolStart(ctx context.Context, toolName string, input json.RawMessage) (context.Context, func(err error, output string))

	// OnGuardrailStart is called before each guardrail execution.
	// input is the text being checked.
	OnGuardrailStart(ctx context.Context, direction string, input string) (context.Context, func(err error, output string))

	// OnMemoryStart is called before memory Load or Save.
	OnMemoryStart(ctx context.Context, operation string, conversationID string) (context.Context, func(err error))

	// OnRetrieverStart is called before Retriever.Retrieve.
	// query is the retrieval query text.
	OnRetrieverStart(ctx context.Context, query string) (context.Context, func(err error, docCount int))

	// OnMaxIterationsExceeded records the max-iterations-exceeded event.
	OnMaxIterationsExceeded(ctx context.Context, limit int)
}

// InvokeSpanParams carries data for the root invocation span.
type InvokeSpanParams struct {
	MaxIterations  int
	ModelID        string // empty if provider doesn't implement ModelIdentifier
	ConversationID string // empty if no memory
	UserMessage    string // the user's input message
	SystemPrompt   string // the agent's system instructions
}

// ProviderCallParams carries data for a provider call span.
type ProviderCallParams struct {
	System       string // system prompt sent to the provider
	MessageCount int    // number of messages in the conversation
}

// SetTracingHook sets the tracing hook. Called by the tracing submodule's Option.
func (a *Agent) SetTracingHook(h TracingHook) {
	a.tracingHook = h
}

// TracingHook returns the agent's tracing hook, or nil if none is set.
func (a *Agent) TracingHook() TracingHook {
	return a.tracingHook
}

// SetLoggerIfNotSet sets the logger only if no custom logger was explicitly
// provided via WithLogger. Called by the tracing submodule to install the
// structured logger automatically.
func (a *Agent) SetLoggerIfNotSet(l Logger) {
	if !a.loggerSet {
		a.logger = l
	}
}
