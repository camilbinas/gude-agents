package agent

import "fmt"

// ProviderError wraps a failure from an LLM provider call.
type ProviderError struct {
	Cause error
}

func (e *ProviderError) Error() string { return "provider error: " + e.Cause.Error() }
func (e *ProviderError) Unwrap() error { return e.Cause }

// ToolError wraps a failure from a tool execution.
type ToolError struct {
	ToolName string
	Cause    error
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("tool %q error: %s", e.ToolName, e.Cause)
}
func (e *ToolError) Unwrap() error { return e.Cause }

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
