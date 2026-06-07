package agent

import (
	"fmt"
	"time"
)

// Logger is an optional interface for logging. Used by conversation strategies
// for error reporting during background operations.
type Logger interface {
	Printf(format string, v ...any)
}

// Option configures the Agent.
type Option func(*Agent) error

// WithName sets an optional name for the agent. The name is used as a
// dimension/attribute in metrics and tracing hooks, making it possible to
// distinguish telemetry from different agents in the same process.
func WithName(name string) Option {
	return func(a *Agent) error {
		a.name = name
		return nil
	}
}

// WithMaxIterations sets the maximum number of call-execute-respond iterations.
func WithMaxIterations(n int) Option {
	return func(a *Agent) error {
		if n < 1 {
			return fmt.Errorf("maxIterations must be >= 1, got %d", n)
		}
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

// WithConversation configures conversation history for multi-turn support.
// The conversationID is used as the default; it can be overridden per-invocation
// using c.WithConversationID on the *Context.
func WithConversation(c Conversation, conversationID string) Option {
	return func(a *Agent) error {
		a.conversation = c
		a.conversationID = conversationID
		return nil
	}
}

// WithSharedConversation configures conversation history without a default conversationID.
// Each invocation must provide a conversationID via c.WithConversationID on the *Context.
// This is the recommended pattern for HTTP servers where a single Agent instance
// serves multiple concurrent conversations.
func WithSharedConversation(c Conversation) Option {
	return func(a *Agent) error {
		a.conversation = c
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
// With InvokeStream, chunks stream in real-time and guardrails run after the
// full response is assembled. A GuardrailError is returned if validation fails.
// With Invoke, the returned text is always the guardrail-processed result.
func WithOutputGuardrail(g ...OutputGuardrail) Option {
	return func(a *Agent) error {
		a.outputGuardrails = append(a.outputGuardrails, g...)
		return nil
	}
}

// WithToolFilter adds tool filter(s) evaluated before each provider call (AND semantics).
// A tool must pass all filters to be included. Accumulates across multiple calls.
func WithToolFilter(filters ...ToolFilter) Option {
	return func(a *Agent) error {
		a.toolFilters = append(a.toolFilters, filters...)
		return nil
	}
}

// WithTokenBudget sets a maximum token budget for each invocation.
// If cumulative token usage exceeds maxTokens, the invocation is aborted
// with ErrTokenBudgetExceeded. A value of 0 means no budget (default).
func WithTokenBudget(maxTokens int) Option {
	return func(a *Agent) error {
		if maxTokens < 0 {
			return fmt.Errorf("token budget must be >= 0, got %d", maxTokens)
		}
		a.tokenBudget = maxTokens
		return nil
	}
}

// WithRateLimiter attaches a RateLimiter to the agent.
// The limiter's Acquire method is called before each provider call (keyed by
// conversation ID), and Record is called after each successful provider call.
func WithRateLimiter(rl *RateLimiter) Option {
	return func(a *Agent) error {
		a.rateLimiter = rl
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

// WithSyncConversation makes the agent call Wait() on the conversation after each Save,
// blocking until any background work (e.g. summarization) is complete before
// returning from Invoke. Only has an effect if the conversation implements ConversationWaiter.
func WithSyncConversation() Option {
	return func(a *Agent) error {
		a.syncConversation = true
		return nil
	}
}

// WithMessageNormalizer sets the normalization strategy.
func WithMessageNormalizer(s NormStrategy) Option {
	return func(a *Agent) error {
		if s < NormMerge || s > NormRemove {
			return fmt.Errorf("invalid normalization strategy: %d", s)
		}
		a.normStrategy = &s
		a.normDisabled = false
		return nil
	}
}

// WithoutMessageNormalizer disables message normalization entirely.
func WithoutMessageNormalizer() Option {
	return func(a *Agent) error {
		a.normDisabled = true
		return nil
	}
}

// WithTimeout sets a per-call timeout for provider calls. Each call to
// ConverseStream gets a context with this deadline. If the provider doesn't
// respond in time, the call is cancelled and returns a context.DeadlineExceeded
// error wrapped in a ProviderError.
// A value of 0 means no timeout (default).
func WithTimeout(d time.Duration) Option {
	return func(a *Agent) error {
		if d < 0 {
			return fmt.Errorf("timeout must be non-negative, got %s", d)
		}
		a.providerTimeout = d
		return nil
	}
}

// WithRetry enables automatic retry with exponential backoff for transient
// provider errors. When a provider call fails, the agent retries up to
// maxRetries times with delays of baseDelay, 2*baseDelay, 4*baseDelay, etc.
// Only errors that are not context cancellation or deadline exceeded are retried.
// A maxRetries of 0 means no retry (default).
func WithRetry(maxRetries int, baseDelay time.Duration) Option {
	return func(a *Agent) error {
		if maxRetries < 0 {
			return fmt.Errorf("maxRetries must be non-negative, got %d", maxRetries)
		}
		if baseDelay < 0 {
			return fmt.Errorf("baseDelay must be non-negative, got %s", baseDelay)
		}
		a.retryMax = maxRetries
		a.retryBaseDelay = baseDelay
		return nil
	}
}

// WithTemperature sets the temperature inference parameter on the agent.
// Temperature controls randomness of LLM output. Valid range: [0.0, 1.0].
func WithTemperature(v float64) Option {
	return func(a *Agent) error {
		if v < 0.0 || v > 1.0 {
			return fmt.Errorf("temperature must be between 0.0 and 1.0, got %f", v)
		}
		if a.inferenceConfig == nil {
			a.inferenceConfig = &InferenceConfig{}
		}
		a.inferenceConfig.Temperature = &v
		return nil
	}
}

// WithTopP sets the top_p inference parameter on the agent.
// TopP controls nucleus sampling probability cutoff. Valid range: [0.0, 1.0].
func WithTopP(v float64) Option {
	return func(a *Agent) error {
		if v < 0.0 || v > 1.0 {
			return fmt.Errorf("top_p must be between 0.0 and 1.0, got %f", v)
		}
		if a.inferenceConfig == nil {
			a.inferenceConfig = &InferenceConfig{}
		}
		a.inferenceConfig.TopP = &v
		return nil
	}
}

// WithTopK sets the top_k inference parameter on the agent.
// TopK limits the number of highest-probability tokens considered. Must be >= 1.
func WithTopK(v int) Option {
	return func(a *Agent) error {
		if v < 1 {
			return fmt.Errorf("top_k must be >= 1, got %d", v)
		}
		if a.inferenceConfig == nil {
			a.inferenceConfig = &InferenceConfig{}
		}
		a.inferenceConfig.TopK = &v
		return nil
	}
}

// WithStopSequences sets the stop sequences inference parameter on the agent.
// Stop sequences cause the LLM to stop producing further tokens when generated.
func WithStopSequences(s []string) Option {
	return func(a *Agent) error {
		if a.inferenceConfig == nil {
			a.inferenceConfig = &InferenceConfig{}
		}
		a.inferenceConfig.StopSequences = s
		return nil
	}
}

// WithMaxTokens sets the max tokens inference parameter on the agent.
// This controls the maximum number of tokens the LLM can generate in a response.
// Must be >= 1. When set, this overrides the provider-level max tokens for every call.
func WithMaxTokens(n int) Option {
	return func(a *Agent) error {
		if n < 1 {
			return fmt.Errorf("max_tokens must be >= 1, got %d", n)
		}
		if a.inferenceConfig == nil {
			a.inferenceConfig = &InferenceConfig{}
		}
		a.inferenceConfig.MaxTokens = &n
		return nil
	}
}

// WithBackgroundNotify registers a callback that is invoked after a Background_Tool's
// Re_Entry_Turn completes successfully. The callback receives the Conversation_ID and
// the final assistant message produced by the reactive turn. The callback is wired onto
// the backgroundRegistry at construction time (see agent.New).
func WithBackgroundNotify(fn func(conversationID, agentMessage string)) Option {
	return func(a *Agent) error {
		a.bgNotify = fn
		return nil
	}
}

// WithHandoffStore configures a durable store for in-flight HandoffRequests.
// When set, the agent automatically persists the HandoffRequest to the store
// whenever ErrHandoffRequested is returned, and deletes it from the store
// after a successful Resume. This makes it safe to store pending handoffs
// across process restarts or in multi-process HTTP servers without managing
// the HandoffRequest lifecycle manually.
func WithHandoffStore(s HandoffStore) Option {
	return func(a *Agent) error {
		if s == nil {
			return fmt.Errorf("WithHandoffStore: store must not be nil")
		}
		a.handoffStore = s
		return nil
	}
}

// WithCaching enables all supported prompt caching. It sets CachingEnabled on
// each provider call (causing providers to automatically inject cache markers
// on DocumentBlocks and system prompts) and enables summary caching on any
// summary conversation strategy attached to the agent.
func WithCaching() Option {
	return func(a *Agent) error {
		a.cachingEnabled = true
		return nil
	}
}
