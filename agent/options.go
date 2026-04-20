package agent

import (
	"fmt"
	"time"
)

// Logger is an optional interface for agent logging.
// Documented in docs/agent-api.md — update when changing interface.
type Logger interface {
	Printf(format string, v ...any)
}

// Option configures the Agent.
// Documented in docs/agent-api.md — update when adding or changing options.
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
		a.loggerSet = true
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

// WithSynchronousMemory makes the agent call Wait() on the memory after each Save,
// blocking until any background work (e.g. summarization) is complete before
// returning from Invoke. Only has an effect if the memory implements MemoryWaiter.
func WithSynchronousMemory() Option {
	return func(a *Agent) error {
		a.syncMemory = true
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
