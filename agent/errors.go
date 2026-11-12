package agent

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// --- Sentinel errors ---

// ErrRateLimitExceeded is returned when a rate limit is exceeded in FailFast mode.
var ErrRateLimitExceeded = errors.New("rate limit exceeded")

// Lease lifecycle errors are returned by rate limiter Commit and Release.
// They are sentinels so callers can use errors.Is without backend coupling.
var (
	ErrRateLimitLeaseTerminal      = errors.New("rate limit lease already terminal")
	ErrRateLimitLeaseCrossTerminal = errors.New("rate limit lease terminal action conflicts")
	ErrRateLimitLeaseUnknown       = errors.New("rate limit lease is unknown")
	ErrRateLimitLeaseExpired       = errors.New("rate limit lease expired")
)

// ErrTokenBudgetExceeded is returned when cumulative token usage exceeds the configured budget.
var ErrTokenBudgetExceeded = errors.New("token budget exceeded")

// ErrMaxIterationsExceeded is returned when the agent loop exhausts its iteration limit
// without producing a final text response.
var ErrMaxIterationsExceeded = errors.New("max iterations exceeded")

// ErrConversationIDRequired is returned when a Runtime configured with a
// ConversationStore is invoked (or resumed) without a non-empty conversation
// ID. The check runs before guardrails, conversation load, retrieval,
// provider calls, and tool execution. Stateless Agents should be constructed
// without a ConversationStore.
var ErrConversationIDRequired = errors.New("agent: conversation ID required when a conversation store is configured")

// ErrRetrieverRequired is returned by RAGAgent when the retriever is nil.
var ErrRetrieverRequired = errors.New("agent: RAG retriever is required")

// ErrStructuredOutputUnsupported is returned as the cause of a
// StructuredOutputError when a configured provider explicitly reports that it
// cannot satisfy structured output through either a named or automatic schema
// tool choice mechanism.
var ErrStructuredOutputUnsupported = errors.New("structured output is unsupported by configured provider capabilities")

// --- Typed errors ---

// ProviderError wraps a failure from an LLM provider call.
//
// Provider adapters classify the underlying SDK/transport error and populate
// the optional fields so callers can implement backoff and failover without
// type-asserting SDK-internal error types:
//
//   - StatusCode is the HTTP status code when the failure came from an HTTP
//     response (0 when unknown, e.g. a transport/network error).
//   - Retryable reports whether retrying the same call, or failing over to
//     another provider, can plausibly succeed. Rate limits (429), server
//     errors (5xx), provider throttling/overload, and network errors are
//     retryable; client errors such as 400/401/403/404 are not.
//   - RetryAfter is the provider-advised minimum wait before a retry, parsed
//     from a Retry-After header when present (0 when not advised).
//
// Use IsRetryable to test an error anywhere in a wrapped chain.
type ProviderError struct {
	Cause      error
	StatusCode int
	Retryable  bool
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string { return "provider error: " + e.Cause.Error() }
func (e *ProviderError) Unwrap() error { return e.Cause }
func (e *ProviderError) Is(target error) bool {
	_, ok := target.(*ProviderError)
	return ok
}

// IsRetryable reports whether err (or any error it wraps) is a ProviderError
// that the adapter classified as retryable. It returns false for a nil error,
// for an unclassified error, and for a ProviderError marked non-retryable.
//
// Context cancellation and deadline-exceeded errors are never retryable:
// IsRetryable returns false for them even when wrapped in a ProviderError,
// since no retry or failover can succeed under a done context.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable
	}
	return false
}

// RetryAfter returns the provider-advised minimum wait before retrying err, or
// 0 when err is not a ProviderError or carries no advice.
func RetryAfter(err error) time.Duration {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.RetryAfter
	}
	return 0
}

// ProviderCreationError wraps a failure from constructing an LLM provider.
// Use errors.Is or errors.As to distinguish provider creation failures from
// runtime provider call failures (ProviderError).
type ProviderCreationError struct {
	Provider string // provider name (e.g. "bedrock", "anthropic", "openai")
	Cause    error
}

func (e *ProviderCreationError) Error() string {
	return fmt.Sprintf("%s provider creation error: %s", e.Provider, e.Cause)
}
func (e *ProviderCreationError) Unwrap() error { return e.Cause }
func (e *ProviderCreationError) Is(target error) bool {
	_, ok := target.(*ProviderCreationError)
	return ok
}

// ToolError wraps a failure from a tool execution.
type ToolError struct {
	ToolName string
	Cause    error
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("tool %q error: %s", e.ToolName, e.Cause)
}
func (e *ToolError) Unwrap() error { return e.Cause }
func (e *ToolError) Is(target error) bool {
	_, ok := target.(*ToolError)
	return ok
}

// GuardrailError indicates a guardrail rejected or failed to process a message.
// Direction is either "input" or "output".
type GuardrailError struct {
	Direction string
	Cause     error
}

func (e *GuardrailError) Error() string {
	return fmt.Sprintf("%s guardrail error: %s", e.Direction, e.Cause)
}
func (e *GuardrailError) Unwrap() error { return e.Cause }
func (e *GuardrailError) Is(target error) bool {
	_, ok := target.(*GuardrailError)
	return ok
}

// MaxIterationsError is returned when the agent loop exhausts its iteration limit.
// It wraps ErrMaxIterationsExceeded for errors.Is compatibility and carries the limit.
type MaxIterationsError struct {
	Limit int
}

func (e *MaxIterationsError) Error() string {
	return fmt.Sprintf("max iterations (%d) exceeded", e.Limit)
}

func (e *MaxIterationsError) Is(target error) bool {
	return target == ErrMaxIterationsExceeded
}

// StructuredOutputError reason values. Match on these consts rather than the
// bare strings; the set is exhaustive for StructuredOutputError.Reason.
const (
	// ReasonNilAgent: structured.Invoke was called with a nil *Runtime.
	ReasonNilAgent = "nil_agent"
	// ReasonUnsupportedCapability: the provider cannot satisfy structured
	// output through either a named or automatic schema tool choice.
	ReasonUnsupportedCapability = "unsupported_capability"
	// ReasonNoToolCall: the model returned no tool call at all.
	ReasonNoToolCall = "no_tool_call"
	// ReasonWrongTool: the model called a tool other than the schema tool.
	ReasonWrongTool = "wrong_tool"
	// ReasonSchemaValidation: the returned JSON failed schema validation.
	ReasonSchemaValidation = "schema_validation"
	// ReasonDeserialize: the returned JSON was invalid or failed to decode
	// into the target type.
	ReasonDeserialize = "deserialize"
)

// StructuredOutputError is returned when structured output fails to produce
// a valid typed response. Reason indicates what went wrong and is one of the
// exported Reason* consts: ReasonNilAgent, ReasonUnsupportedCapability,
// ReasonNoToolCall, ReasonWrongTool, ReasonSchemaValidation, or
// ReasonDeserialize.
type StructuredOutputError struct {
	// Reason is one of the Reason* consts: ReasonNilAgent,
	// ReasonUnsupportedCapability, ReasonNoToolCall, ReasonWrongTool,
	// ReasonSchemaValidation, or ReasonDeserialize.
	Reason string
	Cause  error // underlying error (nil for no_tool_call/wrong_tool/nil_agent)
}

func (e *StructuredOutputError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("structured output: %s: %s", e.Reason, e.Cause)
	}
	return "structured output: " + e.Reason
}

func (e *StructuredOutputError) Unwrap() error { return e.Cause }

func (e *StructuredOutputError) Is(target error) bool {
	_, ok := target.(*StructuredOutputError)
	return ok
}

// ErrPauseStorageRequired is returned when a run needs to pause (tool approval
// or human input) or resume, but the Runtime is not configured with both a
// ConversationStore and an ExecutionStore. A paused run is persisted execution
// state that references the canonical transcript, so pausing needs both. The
// Runtime never creates in-memory stores on its own; select them explicitly, for
// example conversation.NewInMemory() and execution.NewMemory().
var ErrPauseStorageRequired = errors.New("agent: pausing and resuming require both a ConversationStore and an ExecutionStore; configure them with WithConversationStore and WithExecutionStore")
