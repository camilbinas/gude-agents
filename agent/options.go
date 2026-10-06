package agent

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/camilbinas/gude-agents/agent/tool"
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

// WithTools registers a fixed set of tools on the Agent's registry.
func WithTools(tools ...tool.Tool) Option {
	return func(a *Agent) error {
		for _, t := range tools {
			if err := a.toolRegistry.Register(t); err != nil {
				return err
			}
		}
		return nil
	}
}

// WithToolRegistry uses a shared concurrency-safe dynamic tool registry.
func WithToolRegistry(registry *tool.Registry) Option {
	return func(a *Agent) error {
		if registry == nil {
			return fmt.Errorf("tool registry must not be nil")
		}
		a.toolRegistry = registry
		return nil
	}
}

// WithSequentialTools disables concurrent tool execution, running each call
// in a batch one at a time in provider order. Tool calls run in parallel by
// default; use this option when tool handlers are not safe to run
// concurrently or must observe each other's side effects in order.
func WithSequentialTools() Option {
	return func(a *Agent) error {
		a.parallelTools = false
		return nil
	}
}

// WithConversationStore configures conversation persistence. Conversation IDs
// are supplied only by Context.WithConversationID. When a ConversationStore is
// configured, every invocation requires a non-empty ConversationID; an empty
// ID fails with ErrConversationIDRequired before any invocation work runs.
// Stateless Agents should be constructed without a ConversationStore.
func WithConversationStore(c ConversationStore) Option {
	return func(a *Agent) error {
		a.conversation = c
		return nil
	}
}

// WithExecutionStore configures durable execution lifecycle and pause state.
// It requires a ConversationStore because executions reference canonical
// history by cursor rather than storing transcript copies.
func WithExecutionStore(store ExecutionStore) Option {
	return func(a *Agent) error {
		if store == nil {
			return fmt.Errorf("WithExecutionStore: store must not be nil")
		}
		a.executionStore = store
		return nil
	}
}

// WithContextManager configures a model-context projection. It is applied after
// canonical range loading and before every provider call; it never changes the
// append-only conversation log. A ContextManager can use durable
// ContextStateStore state to select a LoadAfter boundary.
func WithContextManager(manager ContextManager) Option {
	return func(a *Agent) error {
		if manager == nil {
			return fmt.Errorf("context manager must not be nil")
		}
		a.contextManager = manager
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
// With Stream / TextStream, text chunks stream live and guardrails run after
// the full response is assembled; a GuardrailError ends the stream if
// validation fails. Result.Text is always the guardrail-processed answer.
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

// WithRateLimiter attaches provider-call rate limiting. Concrete limiters are
// available from the agent/ratelimit package.
func WithRateLimiter(rl RateLimiter) Option {
	return func(a *Agent) error {
		a.rateLimiter = rl
		return nil
	}
}

// WithRetriever attaches a rag.Retriever to the agent.
func WithRetriever(r rag.Retriever) Option {
	return func(a *Agent) error {
		a.retriever = r
		return nil
	}
}

// WithContextFormatter sets a custom rag.ContextFormatter for RAG.
// It defaults to rag.DefaultContextFormatter when not set.
func WithContextFormatter(f rag.ContextFormatter) Option {
	return func(a *Agent) error {
		a.contextFormatter = f
		return nil
	}
}

// WithSyncConversation makes the agent call Flush(ctx) after each successful
// Append, blocking until asynchronous persistence work is complete. It only has an
// effect when the store implements Flusher.
func WithSyncConversation() Option {
	return func(a *Agent) error {
		a.syncConversation = true
		return nil
	}
}

// WithNormalization sets the message normalization strategy.
func WithNormalization(s NormStrategy) Option {
	return func(a *Agent) error {
		if s < NormMerge || s > NormRemove {
			return fmt.Errorf("invalid normalization strategy: %d", s)
		}
		a.normStrategy = &s
		a.normDisabled = false
		return nil
	}
}

// WithoutNormalization disables message normalization entirely.
func WithoutNormalization() Option {
	return func(a *Agent) error {
		a.normDisabled = true
		return nil
	}
}

// WithProviderTimeout sets a timeout for each provider call.
func WithProviderTimeout(d time.Duration) Option {
	return func(a *Agent) error {
		if d < 0 {
			return fmt.Errorf("timeout must be non-negative, got %s", d)
		}
		a.providerTimeout = d
		return nil
	}
}

// WithProviderRetry configures provider retry with exponential backoff.
func WithProviderRetry(maxRetries int, baseDelay time.Duration) Option {
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

// WithMaxOutputTokens sets the maximum provider output tokens.
func WithMaxOutputTokens(n int) Option {
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

// observerCapabilityNames lists every observer capability interface, in the
// order checked by supportsObserver. Used to build a self-documenting error
// when a value implements none of them.
var observerCapabilityNames = []string{
	"InvokeObserver", "IterationObserver", "ModelObserver", "ToolObserver",
	"GuardrailObserver", "ConversationObserver", "RetrievalObserver",
	"AttachmentObserver", "LimitObserver", "ToolLogObserver", "InterruptObserver",
}

// WithObserver registers one observer adapter. observer must implement at
// least one observer capability interface (see Observer); it is repeatable,
// and observers are called in registration order at lifecycle start and
// reverse order at end.
func WithObserver(observer Observer) Option {
	return func(a *Agent) error {
		if !supportsObserver(observer) {
			return fmt.Errorf("WithObserver: %s implements none of %s",
				describeObserverType(observer), strings.Join(observerCapabilityNames, ", "))
		}
		a.observers = append(a.observers, observer)
		return nil
	}
}

func describeObserverType(observer any) string {
	if observer == nil {
		return "nil"
	}
	return reflect.TypeOf(observer).String()
}

func supportsObserver(observer any) bool {
	if observer == nil {
		return false
	}
	value := reflect.ValueOf(observer)
	if (value.Kind() == reflect.Ptr || value.Kind() == reflect.Map || value.Kind() == reflect.Slice || value.Kind() == reflect.Func || value.Kind() == reflect.Interface) && value.IsNil() {
		return false
	}
	switch observer.(type) {
	case InvokeObserver, IterationObserver, ModelObserver, ToolObserver,
		GuardrailObserver, ConversationObserver, RetrievalObserver,
		AttachmentObserver, LimitObserver, ToolLogObserver, InterruptObserver:
		return true
	default:
		return false
	}
}
