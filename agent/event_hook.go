package agent

import (
	"encoding/json"
	"time"
)

// Stop reason constants for OnModelEnd.
const (
	StopReasonEndTurn = "end_turn" // Model produced final text answer
	StopReasonToolUse = "tool_use" // Model requested tool calls
	StopReasonError   = "error"    // Provider call failed
)

// EventHook is an optional interface for real-time UI event delivery.
// Unlike other hooks that serve operational observability, EventHook surfaces
// the actual data flowing through tool calls and model interactions.
//
// Attach an EventHook to a specific invocation via the Context:
//
//	c := agent.Background().WithEventHook(myHook)
//	a.InvokeStream(c, message, streamCB)
type EventHook interface {
	// OnToolCallStart is called before a tool handler is invoked.
	OnToolCallStart(c *Context, toolName string, input json.RawMessage)

	// OnToolCallEnd is called after a tool handler completes.
	// On success, err is nil and output contains the tool's response.
	// On failure, err is non-nil and output is empty.
	OnToolCallEnd(c *Context, toolName string, output string, err error, duration time.Duration)

	// OnThinking is called for each thinking/reasoning chunk emitted by the model.
	OnThinking(c *Context, chunk string)

	// OnModelStart is called before the Provider.ConverseStream call.
	OnModelStart(c *Context)

	// OnModelEnd is called after the Provider call completes.
	// stopReason is one of: "end_turn", "tool_use", "error".
	OnModelEnd(c *Context, stopReason string)

	// OnIterationStart is called at the beginning of each agent loop iteration.
	// iteration is 1-indexed.
	OnIterationStart(c *Context, iteration int)

	// OnIterationEnd is called at the end of each agent loop iteration.
	// toolCount is the number of tool calls executed in this iteration (0 for final).
	// isFinal is true when this iteration produced the final text response.
	OnIterationEnd(c *Context, iteration int, toolCount int, isFinal bool, duration time.Duration)

	// OnMaxIterationsExceeded is called when the agent loop terminates because
	// the configured maximum iteration count was reached without a final answer.
	OnMaxIterationsExceeded(c *Context, limit int)
}

// BaseEventHook provides no-op implementations of all EventHook methods.
// Embed it in your struct to only override the methods you care about.
type BaseEventHook struct{}

func (BaseEventHook) OnToolCallStart(_ *Context, _ string, _ json.RawMessage)                {}
func (BaseEventHook) OnToolCallEnd(_ *Context, _ string, _ string, _ error, _ time.Duration) {}
func (BaseEventHook) OnThinking(_ *Context, _ string)                                        {}
func (BaseEventHook) OnModelStart(_ *Context)                                                {}
func (BaseEventHook) OnModelEnd(_ *Context, _ string)                                        {}
func (BaseEventHook) OnIterationStart(_ *Context, _ int)                                     {}
func (BaseEventHook) OnIterationEnd(_ *Context, _ int, _ int, _ bool, _ time.Duration)       {}
func (BaseEventHook) OnMaxIterationsExceeded(_ *Context, _ int)                              {}
