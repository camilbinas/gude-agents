package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// EventType identifies the kind of Event yielded by Stream / ResumeStream.
type EventType string

// Event types yielded by Stream and ResumeStream.
const (
	// EventStart is the first event of every stream.
	EventStart EventType = "start"
	// EventText carries a live text chunk from the model (Event.Text).
	EventText EventType = "text"
	// EventThinking carries a live thinking/reasoning chunk (Event.Thinking).
	EventThinking EventType = "thinking"
	// EventToolStart is emitted before a tool call runs (Event.Tool).
	EventToolStart EventType = "tool_start"
	// EventToolEnd is emitted after a tool call finished (Event.Tool). It
	// carries the same CallID as the matching EventToolStart.
	EventToolEnd EventType = "tool_end"
	// EventWidget carries a widget emitted by a tool via EmitWidget (Event.Widget).
	EventWidget EventType = "widget"
	// EventInterrupt is emitted when the invocation pauses (Event.Interrupt).
	// It is followed by EventEnd whose Result carries the same Interrupt.
	EventInterrupt EventType = "interrupt"
	// EventCustom carries a user-defined event emitted via EmitEvent (Event.Custom).
	EventCustom EventType = "custom"
	// EventEnd is the last event of every stream. Event.Result is the canonical
	// outcome (identical to what Invoke returns). On failure Event.Error is set
	// and the iterator yields the Go error alongside it.
	EventEnd EventType = "end"

	// Detailed lifecycle events. Only emitted when the invocation Context was
	// configured with WithDetailedEvents (Event.Lifecycle).
	EventIterationStart EventType = "iteration_start"
	EventIterationEnd   EventType = "iteration_end"
	EventModelStart     EventType = "model_start"
	EventModelEnd       EventType = "model_end"
	EventMaxIterations  EventType = "max_iterations"
)

// Event is a tagged union of everything observable during an invocation.
// Type is the discriminator; only the pointer field documented for that type
// is populated. Event is JSON-serializable for SSE / WebSocket transports.
type Event struct {
	Type EventType `json:"type"`
	Time time.Time `json:"time"`

	Text      *TextEvent      `json:"text,omitempty"`
	Thinking  *ThinkingEvent  `json:"thinking,omitempty"`
	Tool      *ToolEvent      `json:"tool,omitempty"`
	Widget    *WidgetEvent    `json:"widget,omitempty"`
	Interrupt *Interrupt      `json:"interrupt,omitempty"`
	Custom    *CustomEvent    `json:"custom,omitempty"`
	Lifecycle *LifecycleEvent `json:"lifecycle,omitempty"`
	Result    *Result         `json:"result,omitempty"`
	Error     *ErrorInfo      `json:"error,omitempty"`
}

// TextEvent is the payload of EventText.
type TextEvent struct {
	Content string `json:"content"`
}

// ThinkingEvent is the payload of EventThinking.
type ThinkingEvent struct {
	Content string `json:"content"`
}

// CustomEvent is the payload of EventCustom.
type CustomEvent struct {
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// WidgetEvent is the payload of EventWidget.
type WidgetEvent struct {
	CallID  string          `json:"call_id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ToolEvent is the payload of EventToolStart and EventToolEnd.
// CallID is the provider tool-use ID and is identical on start and end.
// Input is set on start; Output, Error and Duration are set on end.
type ToolEvent struct {
	CallID   string          `json:"call_id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input,omitempty"`
	Output   string          `json:"output,omitempty"`
	Error    *ErrorInfo      `json:"error,omitempty"`
	Duration time.Duration   `json:"-"` // serialized as duration_ms
}

// MarshalJSON encodes Duration as integer milliseconds ("duration_ms").
func (e ToolEvent) MarshalJSON() ([]byte, error) {
	type alias ToolEvent
	return json.Marshal(struct {
		alias
		DurationMS int64 `json:"duration_ms,omitempty"`
	}{alias(e), e.Duration.Milliseconds()})
}

// UnmarshalJSON decodes the duration_ms field back into Duration.
func (e *ToolEvent) UnmarshalJSON(b []byte) error {
	type alias ToolEvent
	var w struct {
		alias
		DurationMS int64 `json:"duration_ms"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*e = ToolEvent(w.alias)
	e.Duration = time.Duration(w.DurationMS) * time.Millisecond
	return nil
}

// LifecycleEvent is the payload of the detailed lifecycle events.
type LifecycleEvent struct {
	// Iteration is the 1-indexed loop iteration (iteration_*, model_*).
	Iteration int `json:"iteration,omitempty"`
	// ToolCount is the number of tool calls of the iteration (iteration_end).
	ToolCount int `json:"tool_count,omitempty"`
	// IsFinal is true when the iteration produced the final answer (iteration_end).
	IsFinal bool `json:"is_final,omitempty"`
	// StopReason is "end_turn", "tool_use" or "error" (model_end).
	StopReason string `json:"stop_reason,omitempty"`
	// Limit is the iteration limit (max_iterations).
	Limit int `json:"limit,omitempty"`
	// Duration of the iteration or model call (iteration_end, model_end).
	Duration time.Duration `json:"-"` // serialized as duration_ms
}

// MarshalJSON encodes Duration as integer milliseconds ("duration_ms").
func (e LifecycleEvent) MarshalJSON() ([]byte, error) {
	type alias LifecycleEvent
	return json.Marshal(struct {
		alias
		DurationMS int64 `json:"duration_ms,omitempty"`
	}{alias(e), e.Duration.Milliseconds()})
}

// UnmarshalJSON decodes the duration_ms field back into Duration.
func (e *LifecycleEvent) UnmarshalJSON(b []byte) error {
	type alias LifecycleEvent
	var w struct {
		alias
		DurationMS int64 `json:"duration_ms"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*e = LifecycleEvent(w.alias)
	e.Duration = time.Duration(w.DurationMS) * time.Millisecond
	return nil
}

// ErrorInfo is a serializable description of an error.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error codes used in ErrorInfo.
const (
	ErrorCodeCanceled         = "canceled"
	ErrorCodeDeadlineExceeded = "deadline_exceeded"
	ErrorCodeMaxIterations    = "max_iterations"
	ErrorCodeTokenBudget      = "token_budget_exceeded"
	ErrorCodeRateLimit        = "rate_limit_exceeded"
	ErrorCodeGuardrail        = "guardrail"
	ErrorCodeProvider         = "provider_error"
	ErrorCodeStructuredOutput = "structured_output"
	ErrorCodeTool             = "tool_error"
	ErrorCodeToolDenied       = "tool_denied"
	ErrorCodeUnknownTool      = "unknown_tool"
	ErrorCodeInvalidInput     = "invalid_input"
	ErrorCodeInternal         = "internal"
)

// errorInfo classifies a Go error into an ErrorInfo. Returns nil for nil.
func errorInfo(err error) *ErrorInfo {
	if err == nil {
		return nil
	}
	code := ErrorCodeInternal
	switch {
	case errors.Is(err, context.Canceled):
		code = ErrorCodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = ErrorCodeDeadlineExceeded
	case errors.Is(err, ErrMaxIterationsExceeded):
		code = ErrorCodeMaxIterations
	case errors.Is(err, ErrTokenBudgetExceeded):
		code = ErrorCodeTokenBudget
	case errors.Is(err, ErrRateLimitExceeded):
		code = ErrorCodeRateLimit
	case errors.Is(err, &GuardrailError{}):
		code = ErrorCodeGuardrail
	case errors.Is(err, &StructuredOutputError{}):
		code = ErrorCodeStructuredOutput
	case errors.Is(err, &ProviderError{}):
		code = ErrorCodeProvider
	}
	return &ErrorInfo{Code: code, Message: err.Error()}
}

// ---------------------------------------------------------------------------
// eventSink: delivers events to the Stream consumer
// ---------------------------------------------------------------------------

// eventSink serializes delivery of events to a range-over-func consumer.
//
// The engine runs on the consumer's goroutine and calls emit directly. Tool
// calls running on worker goroutines (parallel tools) call enqueue; the engine
// drains the queue from the consumer's goroutine while it waits for the batch
// (see runParallel), so the consumer's loop body only ever runs on its own
// goroutine. When the consumer stops (yield returns false) the sink cancels
// the invocation and drops every later event.
type eventSink struct {
	mu      sync.Mutex // serializes yield; guards stopped/closed
	yield   func(Event, error) bool
	cancel  context.CancelFunc
	stopped bool // consumer returned false
	closed  bool // iterator returned; never yield again

	qmu   sync.Mutex
	queue []Event
	wake  chan struct{}
}

func newEventSink(yield func(Event, error) bool, cancel context.CancelFunc) *eventSink {
	return &eventSink{yield: yield, cancel: cancel, wake: make(chan struct{}, 1)}
}

// emit yields ev to the consumer. Returns false once the consumer stopped.
func (s *eventSink) emit(ev Event) bool {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.closed {
		return false
	}
	if !s.yield(ev, nil) {
		s.stopped = true
		s.cancel()
		return false
	}
	return true
}

// finish yields the terminal event (with err) unless the consumer stopped,
// then closes the sink.
func (s *eventSink) finish(ev Event, err error) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	s.drain()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped && !s.closed {
		s.yield(ev, err)
	}
	s.closed = true
}

// close marks the sink closed so late emitters are dropped.
func (s *eventSink) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *eventSink) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// enqueue buffers ev for delivery from the consumer's goroutine.
func (s *eventSink) enqueue(ev Event) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	s.qmu.Lock()
	s.queue = append(s.queue, ev)
	s.qmu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// drain yields every buffered event. Must run on the consumer's goroutine.
func (s *eventSink) drain() {
	for {
		s.qmu.Lock()
		batch := s.queue
		s.queue = nil
		s.qmu.Unlock()
		if len(batch) == 0 {
			return
		}
		for _, ev := range batch {
			s.emit(ev)
		}
	}
}

// runParallel runs fn(0..n-1) on worker goroutines and, until all of them
// return, drains their queued events on the calling goroutine. A nil sink
// simply waits. It always waits for every worker, so no goroutine outlives it.
func runParallel(s *eventSink, n int, fn func(i int)) {
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { fn(i) })
	}
	if s == nil {
		wg.Wait()
		return
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	for {
		select {
		case <-s.wake:
			s.drain()
		case <-done:
			s.drain()
			return
		}
	}
}
