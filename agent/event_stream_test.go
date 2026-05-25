package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// drainEvents consumes all events from a stream channel and returns them as a slice.
func drainEvents(ch <-chan AgentEvent) []AgentEvent {
	var out []AgentEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

// eventTypes returns just the Type discriminators from a slice of events.
func eventTypes(events []AgentEvent) []EventType {
	out := make([]EventType, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

// findFirst returns the first event matching t, or nil if none.
func findFirst(events []AgentEvent, t EventType) *AgentEvent {
	for i := range events {
		if events[i].Type == t {
			return &events[i]
		}
	}
	return nil
}

// countOf returns the number of events with the given type.
func countOf(events []AgentEvent, t EventType) int {
	n := 0
	for _, e := range events {
		if e.Type == t {
			n++
		}
	}
	return n
}

func TestInvokeEventStream_FinalTextOnlyEmitsExpectedSequence(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "hello world"})
	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	events := drainEvents(a.InvokeEventStream(Background(), "hi"))

	// Must start with InvokeStart and end with InvokeEnd, channel closed.
	if len(events) < 2 {
		t.Fatalf("expected at least InvokeStart/InvokeEnd, got %d events", len(events))
	}
	if events[0].Type != EventInvokeStart {
		t.Errorf("first event = %s, want %s", events[0].Type, EventInvokeStart)
	}
	last := events[len(events)-1]
	if last.Type != EventInvokeEnd {
		t.Errorf("last event = %s, want %s", last.Type, EventInvokeEnd)
	}
	if last.Err != nil {
		t.Errorf("last event Err = %v, want nil", last.Err)
	}

	// Must contain at least one text chunk and the lifecycle events for one iteration.
	if n := countOf(events, EventTextChunk); n == 0 {
		t.Errorf("expected at least one EventTextChunk, got 0")
	}
	for _, want := range []EventType{
		EventIterationStart,
		EventModelStart,
		EventModelEnd,
		EventIterationEnd,
	} {
		if countOf(events, want) != 1 {
			t.Errorf("expected exactly 1 %s, got %d (sequence: %v)", want, countOf(events, want), eventTypes(events))
		}
	}

	// The single ModelEnd should report end_turn (no tool calls).
	if me := findFirst(events, EventModelEnd); me != nil && me.StopReason != StopReasonEndTurn {
		t.Errorf("ModelEnd StopReason = %q, want %q", me.StopReason, StopReasonEndTurn)
	}
}

func TestInvokeEventStream_ToolCallEmitsToolEvents(t *testing.T) {
	// Iteration 1: tool call. Iteration 2: final answer.
	echoTool := tool.NewRaw("echo", "echoes input",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "echoed", nil
		},
	)

	sp := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "t1", Name: "echo", Input: json.RawMessage(`{"q":"hi"}`)},
			},
		},
		&ProviderResponse{Text: "done"},
	)
	a, err := New(sp, prompt.Text("sys"), []tool.Tool{echoTool})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	events := drainEvents(a.InvokeEventStream(Background(), "go"))

	// Tool call lifecycle.
	tcStart := findFirst(events, EventToolCallStart)
	if tcStart == nil {
		t.Fatalf("no EventToolCallStart in stream: %v", eventTypes(events))
	}
	if tcStart.ToolName != "echo" {
		t.Errorf("ToolCallStart.ToolName = %q, want %q", tcStart.ToolName, "echo")
	}
	if string(tcStart.ToolInput) != `{"q":"hi"}` {
		t.Errorf("ToolCallStart.ToolInput = %s, want %s", tcStart.ToolInput, `{"q":"hi"}`)
	}

	tcEnd := findFirst(events, EventToolCallEnd)
	if tcEnd == nil {
		t.Fatalf("no EventToolCallEnd in stream: %v", eventTypes(events))
	}
	if tcEnd.ToolOutput != "echoed" {
		t.Errorf("ToolCallEnd.ToolOutput = %q, want %q", tcEnd.ToolOutput, "echoed")
	}

	// First iteration ModelEnd should be tool_use, second should be end_turn.
	var stopReasons []string
	for _, e := range events {
		if e.Type == EventModelEnd {
			stopReasons = append(stopReasons, e.StopReason)
		}
	}
	if len(stopReasons) != 2 {
		t.Fatalf("expected 2 ModelEnd events, got %d", len(stopReasons))
	}
	if stopReasons[0] != StopReasonToolUse {
		t.Errorf("first ModelEnd StopReason = %q, want %q", stopReasons[0], StopReasonToolUse)
	}
	if stopReasons[1] != StopReasonEndTurn {
		t.Errorf("second ModelEnd StopReason = %q, want %q", stopReasons[1], StopReasonEndTurn)
	}
}

// errProvider returns a fixed error from ConverseStream.
type errProvider struct{ err error }

func (errProvider) Name() string { return "err" }
func (p errProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return nil, p.err
}
func (p errProvider) ConverseStream(_ context.Context, _ ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	return nil, p.err
}

func TestInvokeEventStream_ProviderErrorSurfacesOnInvokeEnd(t *testing.T) {
	wantErr := errors.New("boom")
	a, err := New(errProvider{err: wantErr}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	events := drainEvents(a.InvokeEventStream(Background(), "hi"))
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	last := events[len(events)-1]
	if last.Type != EventInvokeEnd {
		t.Fatalf("last event = %s, want %s", last.Type, EventInvokeEnd)
	}
	if !errors.Is(last.Err, wantErr) {
		t.Errorf("InvokeEnd.Err = %v, want it to wrap %v", last.Err, wantErr)
	}
}

// chainCheckEventHook records callbacks so we can prove the upstream EventHook
// still fires when InvokeEventStream is used.
type chainCheckEventHook struct {
	BaseEventHook
	starts int
	ends   int
}

func (h *chainCheckEventHook) OnModelStart(_ *Context)         { h.starts++ }
func (h *chainCheckEventHook) OnModelEnd(_ *Context, _ string) { h.ends++ }

func TestInvokeEventStream_PreservesUpstreamEventHook(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "ok"})
	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	upstream := &chainCheckEventHook{}
	c := Background().WithEventHook(upstream)

	events := drainEvents(a.InvokeEventStream(c, "hi"))
	if last := events[len(events)-1]; last.Err != nil {
		t.Fatalf("invocation failed: %v", last.Err)
	}
	if upstream.starts != 1 || upstream.ends != 1 {
		t.Errorf("upstream hook starts=%d ends=%d, want 1/1", upstream.starts, upstream.ends)
	}
}

func TestInvokeEventStream_WithEventStreamBuffer(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "hi"})
	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// Custom small buffer — agent loop will back-pressure but still succeed
	// since we drain immediately.
	events := drainEvents(a.InvokeEventStream(Background(), "hi", WithEventStreamBuffer(2)))
	if len(events) == 0 {
		t.Fatal("expected events")
	}
	if last := events[len(events)-1]; last.Type != EventInvokeEnd || last.Err != nil {
		t.Fatalf("expected clean InvokeEnd, got type=%s err=%v", last.Type, last.Err)
	}

	// Zero / negative buffer falls back to DefaultEventStreamBuffer — exercise
	// just to make sure it doesn't make() a 0-length channel and deadlock.
	sp2 := newScriptedProvider(&ProviderResponse{Text: "hi"})
	a2, err := New(sp2, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	events2 := drainEvents(a2.InvokeEventStream(Background(), "hi", WithEventStreamBuffer(0)))
	if last := events2[len(events2)-1]; last.Type != EventInvokeEnd || last.Err != nil {
		t.Fatalf("zero buffer: expected clean InvokeEnd, got type=%s err=%v", last.Type, last.Err)
	}
}

// TestInvokeEventStream_DoesNotMutateCallerContext verifies that the caller's
// *Context retains its original EventHook (or nil) after InvokeEventStream
// returns, so the same *Context can be safely reused for further invocations.
func TestInvokeEventStream_DoesNotMutateCallerContext(t *testing.T) {
	sp := newScriptedProvider(
		&ProviderResponse{Text: "first"},
		&ProviderResponse{Text: "second"},
	)
	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	upstream := &chainCheckEventHook{}
	c := Background().WithEventHook(upstream)

	// First call via InvokeEventStream — this used to overwrite c.eventHook.
	events := drainEvents(a.InvokeEventStream(c, "one"))
	if last := events[len(events)-1]; last.Err != nil {
		t.Fatalf("first invocation failed: %v", last.Err)
	}

	// Caller's context must still hold the original upstream hook.
	if got := c.EventHook(); got != upstream {
		t.Fatalf("caller context EventHook was replaced: got %T, want *chainCheckEventHook", got)
	}

	// Second call on the same context must work cleanly. If the previous run
	// had left a closed-channel hook in place, this would panic on the first
	// OnModelStart call.
	if _, err := a.Invoke(c, "two"); err != nil {
		t.Fatalf("second invocation failed: %v", err)
	}

	// Both invocations should have routed through the upstream hook.
	if upstream.starts < 2 || upstream.ends < 2 {
		t.Errorf("upstream hook starts=%d ends=%d, want >= 2/2", upstream.starts, upstream.ends)
	}
}
