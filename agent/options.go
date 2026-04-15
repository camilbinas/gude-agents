package agent

// Logger is an optional interface for agent logging.
// Documented in docs/agent-api.md — update when changing interface.
type Logger interface {
	Printf(format string, v ...any)
}

// Option configures the Agent.
// Documented in docs/agent-api.md — update when adding or changing options.
type Option func(*Agent) error

// WithMaxIterations sets the maximum number of call-execute-respond iterations.
func WithMaxIterations(n int) Option {
	return func(a *Agent) error {
		a.maxIterations = n
		return nil
	}
}

// WithParallelToolExecution enables concurrent tool execution.
func WithParallelToolExecution() Option {
	return func(a *Agent) error {
		a.parallelTools = true
		return nil
	}
}

// WithMemory configures conversation memory for multi-turn support.
// The conversationID is used as the default; it can be overridden per-invocation
// using WithConversationID on the context.
func WithMemory(m Memory, conversationID string) Option {
	return func(a *Agent) error {
		a.memory = m
		a.conversationID = conversationID
		return nil
	}
}

// WithSharedMemory configures conversation memory without a default conversationID.
// Each invocation must provide a conversationID via WithConversationID on the context.
// This is the recommended pattern for HTTP servers where a single Agent instance
// serves multiple concurrent conversations.
func WithSharedMemory(m Memory) Option {
	return func(a *Agent) error {
		a.memory = m
		return nil
	}
}

// WithMiddleware adds middleware(s) that wrap tool execution.
func WithMiddleware(mws ...Middleware) Option {
	return func(a *Agent) error {
		a.middlewares = append(a.middlewares, mws...)
		return nil
	}
}

// WithInputGuardrail adds input guardrail(s) applied before sending to the Provider.
func WithInputGuardrail(g ...InputGuardrail) Option {
	return func(a *Agent) error {
		a.inputGuardrails = append(a.inputGuardrails, g...)
		return nil
	}
}

// WithOutputGuardrail adds output guardrail(s) applied to the final response.
func WithOutputGuardrail(g ...OutputGuardrail) Option {
	return func(a *Agent) error {
		a.outputGuardrails = append(a.outputGuardrails, g...)
		return nil
	}
}

// WithLogger sets an optional logger for the agent.
func WithLogger(l Logger) Option {
	return func(a *Agent) error {
		a.logger = l
		return nil
	}
}

// WithTokenBudget sets a maximum token budget for each invocation.
// If cumulative token usage exceeds maxTokens, the invocation is aborted
// with ErrTokenBudgetExceeded. A value of 0 means no budget (default).
func WithTokenBudget(maxTokens int) Option {
	return func(a *Agent) error {
		a.tokenBudget = maxTokens
		return nil
	}
}

// WithRetriever attaches a Retriever to the agent for RAG.
func WithRetriever(r Retriever) Option {
	return func(a *Agent) error {
		a.retriever = r
		return nil
	}
}

// WithContextFormatter sets a custom ContextFormatter for RAG.
// Defaults to DefaultContextFormatter when not set.
func WithContextFormatter(f ContextFormatter) Option {
	return func(a *Agent) error {
		a.contextFormatter = f
		return nil
	}
}

// WithThinkingCallback sets a callback that receives thinking/reasoning chunks in real-time.
// The callback is called as the model reasons, before the final answer is streamed.
// Only fires when the provider has thinking enabled (e.g. WithThinking, WithReasoningEffort).
func WithThinkingCallback(cb ThinkingCallback) Option {
	return func(a *Agent) error {
		a.thinkingCallback = cb
		return nil
	}
}
