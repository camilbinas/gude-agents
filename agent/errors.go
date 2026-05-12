package agent

import (
	"errors"
	"fmt"
)

// --- Sentinel errors ---

// ErrRateLimitExceeded is returned when a rate limit is exceeded in FailFast mode.
var ErrRateLimitExceeded = errors.New("rate limit exceeded")

// ErrTokenBudgetExceeded is returned when cumulative token usage exceeds the configured budget.
var ErrTokenBudgetExceeded = errors.New("token budget exceeded")

// ErrHandoffRequested is returned when an agent calls the handoff tool.
// The caller should inspect the HandoffRequest via GetHandoffRequest,
// collect the needed input, then call Agent.Resume to continue.
var ErrHandoffRequested = errors.New("handoff requested")

// ErrLoopStopped is returned by RunLoop when a ToolResultInterceptor signals stop.
var ErrLoopStopped = errors.New("loop stopped by interceptor")

// ErrMaxIterationsExceeded is returned when the agent loop exhausts its iteration limit
// without producing a final text response.
var ErrMaxIterationsExceeded = errors.New("max iterations exceeded")

// --- Typed errors ---

// ProviderError wraps a failure from an LLM provider call.
type ProviderError struct {
	Cause error
}

func (e *ProviderError) Error() string { return "provider error: " + e.Cause.Error() }
func (e *ProviderError) Unwrap() error { return e.Cause }
func (e *ProviderError) Is(target error) bool {
	_, ok := target.(*ProviderError)
	return ok
}

// ProviderCreationError wraps a failure from constructing an LLM provider.
// Use errors.Is or errors.As to distinguish provider creation failures from
// runtime provider call failures (ProviderError).
type ProviderCreationError struct {
	Provider string // provider name (e.g. "bedrock", "anthropic", "openai", "gemini")
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

// StructuredOutputError is returned when InvokeStructured fails to produce
// a valid typed response. Reason indicates what went wrong.
type StructuredOutputError struct {
	Reason string // "no_tool_call", "wrong_tool", "deserialize"
	Cause  error  // underlying error (nil for no_tool_call/wrong_tool)
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
