package agent

import "fmt"

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
