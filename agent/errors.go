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

// ProviderError wraps a failure from an LLM provider call. Adapters set the
// optional fields so callers can back off and fail over; use IsRetryable to
// test an error anywhere in a wrapped chain.
type ProviderError struct {
	Cause      error
	StatusCode int           // HTTP status, 0 when unknown (e.g. network error)
	Retryable  bool          // retry or failover can plausibly succeed
	RetryAfter time.Duration // provider-advised wait, 0 when not advised
}

func (e *ProviderError) Error() string { return "provider error: " + e.Cause.Error() }
func (e *ProviderError) Unwrap() error { return e.Cause }
func (e *ProviderError) Is(target error) bool {
	_, ok := target.(*ProviderError)
	return ok
}

// IsRetryable reports whether err wraps a ProviderError the adapter marked
// retryable. Context cancellation and deadline-exceeded always return false,
// even when wrapped, since no retry can succeed under a done context.
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

// StructuredOutputError.Reason values; match on these consts, not the strings.
const (
	ReasonNilAgent              = "nil_agent"
	ReasonUnsupportedCapability = "unsupported_capability"
	ReasonNoToolCall            = "no_tool_call"
	ReasonWrongTool             = "wrong_tool"
	ReasonSchemaValidation      = "schema_validation"
	ReasonDeserialize           = "deserialize"
)

// StructuredOutputError is returned when structured output fails to produce a
// valid typed response. Reason is one of the Reason* consts above.
type StructuredOutputError struct {
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
