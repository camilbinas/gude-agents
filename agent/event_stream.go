package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EventType identifies the kind of AgentEvent emitted on an InvokeEventStream
// channel. Consumers should switch on Type and read only the fields documented
// for that variant.
type EventType string

// Event types emitted on an InvokeEventStream channel.
const (
	// EventInvokeStart is emitted once at the very beginning of an invocation,
	// before any iterations or provider calls.
	EventInvokeStart EventType = "invoke_start"

	// EventIterationStart is emitted at the start of each agent loop iteration.
	// Read Iteration for the 1-indexed iteration number.
	EventIterationStart EventType = "iteration_start"

	// EventModelStart is emitted just before each Provider.ConverseStream call.
	EventModelStart EventType = "model_start"

	// EventTextChunk is emitted for each incremental text chunk streamed from
	// the model on the final iteration. Read TextChunk for the chunk content.
	EventTextChunk EventType = "text_chunk"

	// EventThinkingChunk is emitted for each thinking/reasoning chunk streamed
	// from the model when extended thinking is enabled. Read ThinkingChunk.
	EventThinkingChunk EventType = "thinking_chunk"

	// EventToolCallStart is emitted before a tool handler is invoked. Read
	// ToolName and ToolInput for the call details.
	EventToolCallStart EventType = "tool_call_start"

	// EventToolCallEnd is emitted after a tool handler completes. Read
	// ToolName, ToolOutput, Err, and Duration. On error, ToolOutput is empty
	// and Err is non-nil.
	EventToolCallEnd EventType = "tool_call_end"

	// EventModelEnd is emitted after each Provider call completes. Read
	// StopReason ("end_turn", "tool_use", "error").
	EventModelEnd EventType = "model_end"

	// EventIterationEnd is emitted at the end of each agent loop iteration.
	// Read Iteration, ToolCount, IsFinal, Duration.
	EventIterationEnd EventType = "iteration_end"

	// EventMaxIterations is emitted when the loop terminates because the
	// configured maximum iteration count was reached without a final answer.
	// Read IterationLimit.
	EventMaxIterations EventType = "max_iterations_exceeded"

	// EventInvokeEnd is the final event on the channel, emitted exactly once.
	// Read Err for the invocation error (nil on success) and Usage for the
	// cumulative token usage. The channel is closed immediately after.
	EventInvokeEnd EventType = "invoke_end"

	// EventWidget is emitted when a tool handler produces a WidgetBlock via
	// Context.EmitWidget. It is delivered before the corresponding EventToolCallEnd
	// for the same tool call. Read WidgetType and WidgetPayload for the block data.
	EventWidget EventType = "widget"

	// EventHandoffRequested is emitted by InvokeEventStream when the agent loop
	// returns ErrHandoffRequested. Read HandoffReason and HandoffQuestion for the
	// human-facing ask. The HandoffRequest itself (including the full message
	// snapshot) is available via GetHandoffRequest on the cloned context that
	// InvokeEventStream uses internally; callers that need cross-process
	// durability should call GetHandoffRequest on their own *Context after
	// receiving this event (or use WithHandoffStore for automatic persistence).
	// This event is emitted before the terminal EventInvokeEnd.
	EventHandoffRequested EventType = "handoff_requested"

	// EventToolApprovalRequired is emitted by InvokeEventStream when a tool
	// marked with RequiresApproval is called by the LLM. Read ApprovalToolName
	// and ApprovalToolInput for the pending call details. The full ApprovalRequest
	// (including the message snapshot) is available via GetApprovalRequest on
	// the caller's *Context. This event is emitted before the terminal EventInvokeEnd.
	EventToolApprovalRequired EventType = "tool_approval_required"

	// EventCustom carries a user-defined event emitted from inside a tool
	// handler, middleware, or graph node via Context.EmitEvent. The payload
	// is opaque JSON; the receiver typically discriminates on CustomName.
	// The runtime never emits this variant itself.
	EventCustom EventType = "custom"
)

// AgentEvent is a tagged union of everything observable during an invocation,
// delivered via Agent.InvokeEventStream. The Type field is the discriminator;
// only the fields documented for that variant are populated.
//
// AgentEvent is intentionally a flat struct rather than an interface to make
// it trivial to serialize for SSE / WebSocket / JSON transport.
type AgentEvent struct {
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`

	// Iteration data (EventIterationStart, EventIterationEnd, EventMaxIterations).
	Iteration      int           `json:"iteration,omitempty"`
	IterationLimit int           `json:"iteration_limit,omitempty"`
	ToolCount      int           `json:"tool_count,omitempty"`
	IsFinal        bool          `json:"is_final,omitempty"`
	Duration       time.Duration `json:"duration,omitempty"`

	// Streaming chunks (EventTextChunk, EventThinkingChunk).
	TextChunk     string `json:"text_chunk,omitempty"`
	ThinkingChunk string `json:"thinking_chunk,omitempty"`

	// Tool call data (EventToolCallStart, EventToolCallEnd).
	ToolName   string          `json:"tool_name,omitempty"`
	ToolInput  json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput string          `json:"tool_output,omitempty"`
	// PrincipalID is the ID of the principal that triggered the tool call.
	// Populated on EventToolCallStart only; correlate with ToolName for attribution.
	PrincipalID string `json:"principal_id,omitempty"`

	// Model lifecycle (EventModelEnd).
	StopReason string `json:"stop_reason,omitempty"`

	// Invocation result (EventInvokeEnd).
	Usage TokenUsage `json:"usage,omitempty"`
	Err   error      `json:"-"` // not JSON-serializable; stringify at transport layer

	// Custom event payload (EventCustom). CustomName is a free-form, dot-
	// namespaced tag chosen by the emitter (e.g. "rag.retrieved",
	// "score.computed"). CustomPayload is the JSON-encoded user payload.
	// Both are populated only when Type is EventCustom.
	CustomName    string          `json:"custom_name,omitempty"`
	CustomPayload json.RawMessage `json:"custom_payload,omitempty"`

	// Widget event payload (EventWidget). Both fields are populated only when
	// Type is EventWidget.
	WidgetType    string          `json:"widget_type,omitempty"`
	WidgetPayload json.RawMessage `json:"widget_payload,omitempty"`

	// Handoff event payload (EventHandoffRequested). Both fields are populated
	// only when Type is EventHandoffRequested.
	HandoffReason   string `json:"handoff_reason,omitempty"`
	HandoffQuestion string `json:"handoff_question,omitempty"`

	// Tool approval event payload (EventToolApprovalRequired). Both fields are
	// populated only when Type is EventToolApprovalRequired.
	ApprovalToolName  string          `json:"approval_tool_name,omitempty"`
	ApprovalToolInput json.RawMessage `json:"approval_tool_input,omitempty"`
}

// DefaultEventStreamBuffer is the default buffer size for InvokeEventStream's
// channel. A modest buffer absorbs short consumer stalls without blocking the
// agent loop, while still applying back-pressure if the consumer falls behind.
const DefaultEventStreamBuffer = 64

// EventStreamOption configures InvokeEventStream behavior.
type EventStreamOption func(*eventStreamConfig)

type eventStreamConfig struct {
	buffer int
}

// WithEventStreamBuffer sets the channel buffer size for InvokeEventStream.
// Larger buffers reduce the chance of blocking the agent loop on slow consumers
// at the cost of more in-flight memory; smaller buffers (including 0) increase
// back-pressure and keep events closer to real-time at the consumer.
//
// A negative or zero value falls back to DefaultEventStreamBuffer; pass a
// positive value to override.
func WithEventStreamBuffer(n int) EventStreamOption {
	return func(c *eventStreamConfig) { c.buffer = n }
}

// InvokeEventStream runs the agent loop and returns a channel of events
// representing everything that happens during the invocation: text chunks,
// thinking chunks, tool calls, model lifecycle, and the final result.
//
// The channel is closed exactly once, after a single EventInvokeEnd event
// carrying the final error (nil on success) and cumulative TokenUsage.
//
// Consumers must drain the channel to completion. If the consumer returns
// early (e.g. its surrounding context is cancelled), the agent loop will
// block on send once the buffer fills. To stop the agent in that case,
// cancel the context backing the *Context passed in.
//
// Use options like WithEventStreamBuffer to tune channel behavior.
//
// InvokeEventStream coexists with EventHook: if c.EventHook() is set, those
// callbacks still fire. Internally, this method clones the context and
// installs a fan-in EventHook that wraps the user-supplied one (if any),
// so the caller's *Context is never mutated and remains safe to reuse for
// other invocations.
func (a *Agent) InvokeEventStream(c *Context, userMessage string, opts ...EventStreamOption) <-chan AgentEvent {
	cfg := &eventStreamConfig{buffer: DefaultEventStreamBuffer}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.buffer <= 0 {
		cfg.buffer = DefaultEventStreamBuffer
	}

	ch := make(chan AgentEvent, cfg.buffer)

	// Clone the caller's *Context so we don't mutate their state. Without this,
	// calling InvokeEventStream installs the fan-in hook on the caller's
	// context, which then bleeds into any later Invoke / InvokeStream / parallel
	// InvokeEventStream calls sharing the same *Context — at best confusing,
	// at worst a "send on closed channel" panic on a second invocation.
	streamC := c.Clone()
	upstream := streamC.EventHook()
	streamC.WithEventHook(&eventStreamHook{ch: ch, next: upstream})

	// StreamCallback fans text chunks into the same channel.
	streamCB := func(chunk string) {
		ch <- AgentEvent{
			Type:      EventTextChunk,
			Timestamp: time.Now(),
			TextChunk: chunk,
		}
	}

	go func() {
		defer close(ch)

		// Convert a panic in the agent loop into a terminal EventInvokeEnd
		// so consumers always see a clean shutdown event before the channel
		// closes. The panic is otherwise swallowed by the goroutine boundary.
		var panicErr error
		defer func() {
			if r := recover(); r != nil {
				panicErr = fmt.Errorf("agent: panic in InvokeEventStream: %v", r)
			}
			// NOTE: usage is not written back to the caller's *Context. The
			// caller's context is shared with other invocations and writing
			// to its usage field would race. Read Usage off the terminal
			// EventInvokeEnd instead.
			ch <- AgentEvent{
				Type:      EventInvokeEnd,
				Timestamp: time.Now(),
				Err:       panicErr,
				Usage:     streamC.Usage(),
			}
		}()

		ch <- AgentEvent{Type: EventInvokeStart, Timestamp: time.Now()}

		err := a.InvokeStream(streamC, userMessage, streamCB)
		if err != nil {
			panicErr = err
			// Emit a dedicated event so consumers don't have to inspect EventInvokeEnd.Err.
			if errors.Is(err, ErrHandoffRequested) {
				ev := AgentEvent{
					Type:      EventHandoffRequested,
					Timestamp: time.Now(),
				}
				if hr, ok := GetHandoffRequest(streamC); ok {
					ev.HandoffReason = hr.Reason
					ev.HandoffQuestion = hr.Question
				}
				ch <- ev
			} else if errors.Is(err, ErrToolApprovalRequired) {
				ev := AgentEvent{
					Type:      EventToolApprovalRequired,
					Timestamp: time.Now(),
				}
				if ar, ok := GetApprovalRequest(streamC); ok {
					ev.ApprovalToolName = ar.ToolName
					if ar.ToolInput != nil {
						inputCopy := make(json.RawMessage, len(ar.ToolInput))
						copy(inputCopy, ar.ToolInput)
						ev.ApprovalToolInput = inputCopy
					}
				}
				ch <- ev
			}
		}
	}()

	return ch
}

// eventStreamHook adapts EventHook callbacks into AgentEvents on a channel.
// It optionally chains to a user-supplied upstream hook so existing observers
// keep working when InvokeEventStream is used.
type eventStreamHook struct {
	ch   chan<- AgentEvent
	next EventHook // optional chain target, may be nil
}

func (h *eventStreamHook) OnIterationStart(c *Context, iteration int) {
	h.ch <- AgentEvent{
		Type:      EventIterationStart,
		Timestamp: time.Now(),
		Iteration: iteration,
	}
	if h.next != nil {
		h.next.OnIterationStart(c, iteration)
	}
}

func (h *eventStreamHook) OnIterationEnd(c *Context, iteration int, toolCount int, isFinal bool, duration time.Duration) {
	h.ch <- AgentEvent{
		Type:      EventIterationEnd,
		Timestamp: time.Now(),
		Iteration: iteration,
		ToolCount: toolCount,
		IsFinal:   isFinal,
		Duration:  duration,
	}
	if h.next != nil {
		h.next.OnIterationEnd(c, iteration, toolCount, isFinal, duration)
	}
}

func (h *eventStreamHook) OnModelStart(c *Context) {
	h.ch <- AgentEvent{Type: EventModelStart, Timestamp: time.Now()}
	if h.next != nil {
		h.next.OnModelStart(c)
	}
}

func (h *eventStreamHook) OnModelEnd(c *Context, stopReason string) {
	h.ch <- AgentEvent{
		Type:       EventModelEnd,
		Timestamp:  time.Now(),
		StopReason: stopReason,
	}
	if h.next != nil {
		h.next.OnModelEnd(c, stopReason)
	}
}

func (h *eventStreamHook) OnThinking(c *Context, chunk string) {
	h.ch <- AgentEvent{
		Type:          EventThinkingChunk,
		Timestamp:     time.Now(),
		ThinkingChunk: chunk,
	}
	if h.next != nil {
		h.next.OnThinking(c, chunk)
	}
}

func (h *eventStreamHook) OnToolCallStart(c *Context, toolName string, input json.RawMessage) {
	// Copy input so a downstream mutation can't corrupt the channel event.
	var inputCopy json.RawMessage
	if input != nil {
		inputCopy = make(json.RawMessage, len(input))
		copy(inputCopy, input)
	}
	ev := AgentEvent{
		Type:      EventToolCallStart,
		Timestamp: time.Now(),
		ToolName:  toolName,
		ToolInput: inputCopy,
	}
	if p, ok := GetTyped[Principal](c, principalKey{}); ok {
		ev.PrincipalID = p.ID
	}
	h.ch <- ev
	if h.next != nil {
		h.next.OnToolCallStart(c, toolName, input)
	}
}

func (h *eventStreamHook) OnToolCallEnd(c *Context, toolName string, output string, err error, duration time.Duration) {
	ev := AgentEvent{
		Type:       EventToolCallEnd,
		Timestamp:  time.Now(),
		ToolName:   toolName,
		ToolOutput: output,
		Err:        err,
		Duration:   duration,
	}
	h.ch <- ev
	if h.next != nil {
		h.next.OnToolCallEnd(c, toolName, output, err, duration)
	}
}

func (h *eventStreamHook) OnMaxIterationsExceeded(c *Context, limit int) {
	h.ch <- AgentEvent{
		Type:           EventMaxIterations,
		Timestamp:      time.Now(),
		IterationLimit: limit,
	}
	if h.next != nil {
		h.next.OnMaxIterationsExceeded(c, limit)
	}
}

// CustomEventEmitter is an optional companion interface to EventHook. Hooks
// that want to receive user-defined events emitted via Context.EmitEvent
// implement this method in addition to the EventHook interface. Hooks that
// do not implement it simply drop custom events on the floor — the runtime
// itself never emits them, so there is no breakage risk.
//
// The runtime's built-in eventStreamHook (used by InvokeEventStream)
// implements this so custom events flow into the same channel as built-in
// events. A user-supplied EventHook can opt in by adding an
// OnCustomEvent(c *Context, name string, payload json.RawMessage) method.
type CustomEventEmitter interface {
	OnCustomEvent(c *Context, name string, payload json.RawMessage)
}

// OnCustomEvent forwards a user-defined event to the channel and, if the
// chained user hook also implements CustomEventEmitter, to that hook too.
func (h *eventStreamHook) OnCustomEvent(c *Context, name string, payload json.RawMessage) {
	// Defensive copy: the caller may reuse or mutate the slice after returning.
	var payloadCopy json.RawMessage
	if payload != nil {
		payloadCopy = make(json.RawMessage, len(payload))
		copy(payloadCopy, payload)
	}
	h.ch <- AgentEvent{
		Type:          EventCustom,
		Timestamp:     time.Now(),
		CustomName:    name,
		CustomPayload: payloadCopy,
	}
	if next, ok := h.next.(CustomEventEmitter); ok {
		next.OnCustomEvent(c, name, payload)
	}
}
