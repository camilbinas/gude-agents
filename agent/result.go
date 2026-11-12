package agent

// StopReason explains why an invocation ended without an error.
type StopReason string

const (
	// StopEndTurn means the model produced a final answer.
	StopEndTurn StopReason = "end_turn"
	// StopInterrupt means the invocation paused and Result.Interrupt describes
	// what is needed to continue via Runtime.Resume.
	StopInterrupt StopReason = "interrupt"
	// StopMaxTokens means the final answer was cut off at the model's output
	// token limit or context window. Result.Text holds the partial answer.
	// Raise the max tokens inference setting or shorten the conversation.
	StopMaxTokens StopReason = "max_tokens"
	// StopContentFilter means a provider safety filter, guardrail or model
	// refusal ended the final answer. Result.Text holds whatever was produced.
	StopContentFilter StopReason = "content_filter"
)

// stopReasonFor maps the final model response's stop reason onto the
// invocation stop reason. Unknown or natural endings are StopEndTurn.
func stopReasonFor(resp *ModelResponse) StopReason {
	switch {
	case resp == nil:
		return StopEndTurn
	case resp.StopReason.Truncated():
		return StopMaxTokens
	case resp.StopReason == ModelStopContentFilter:
		return StopContentFilter
	default:
		return StopEndTurn
	}
}

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
	// StopReason is StopEndTurn, StopInterrupt, StopMaxTokens or
	// StopContentFilter. Check for StopMaxTokens before treating Text as a
	// complete answer.
	StopReason StopReason `json:"stop_reason,omitempty"`
	// Interrupt is set when StopReason is StopInterrupt.
	Interrupt *Interrupt `json:"interrupt,omitempty"`
	// Metadata carries provider-specific extras of the final model response
	// (e.g. "thinking"). May be nil.
	Metadata map[string]any `json:"metadata,omitempty"`
}
