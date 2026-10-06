package agent

// StopReason explains why an invocation ended without an error.
type StopReason string

const (
	// StopEndTurn means the model produced a final answer.
	StopEndTurn StopReason = "end_turn"
	// StopInterrupt means the invocation paused and Result.Interrupt describes
	// what is needed to continue via Agent.Resume.
	StopInterrupt StopReason = "interrupt"
)

// Result is the outcome of one invocation (Invoke, Resume, or the EventEnd
// event of Stream / ResumeStream).
//
// Max iterations, token budget, guardrail and provider failures are reported
// as errors, not stop reasons. On error the returned Result still carries the
// token usage accumulated so far.
type Result struct {
	// ExecutionID identifies this invocation's durable or local execution.
	ExecutionID string `json:"execution_id,omitempty"`
	// Text is the final assistant answer after output guardrails.
	// Empty when the invocation was interrupted.
	Text string `json:"text"`
	// Usage is the cumulative token usage of the invocation.
	Usage TokenUsage `json:"usage"`
	// StopReason is StopEndTurn or StopInterrupt.
	StopReason StopReason `json:"stop_reason,omitempty"`
	// Interrupt is set when StopReason is StopInterrupt.
	Interrupt *Interrupt `json:"interrupt,omitempty"`
	// Metadata carries provider-specific extras of the final model response
	// (e.g. "thinking"). May be nil.
	Metadata map[string]any `json:"metadata,omitempty"`
}
