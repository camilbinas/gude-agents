package agent

import (
	"encoding/json"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// EmitWidget edge cases
// ---------------------------------------------------------------------------

// TestEmitWidget_NilPayload verifies that EmitWidget with a nil Payload
// succeeds (returns nil error) and stores a WidgetBlock with nil Payload
// in the accumulator.
// Requirements: 1.3, 4.1, 4.3
func TestEmitWidget_NilPayload(t *testing.T) {
	c := Background()

	// Inject a fresh accumulator so EmitWidget can store the block.
	acc := &widgetAccumulator{}
	c.Set(widgetAccumulatorKey{}, acc)

	block := WidgetBlock{Type: "chart", Payload: nil}
	if err := c.EmitWidget(block); err != nil {
		t.Fatalf("EmitWidget with nil Payload returned unexpected error: %v", err)
	}

	drained := acc.drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 block in accumulator, got %d", len(drained))
	}
	if drained[0].Type != "chart" {
		t.Errorf("expected Type=%q, got %q", "chart", drained[0].Type)
	}
	if drained[0].Payload != nil {
		t.Errorf("expected nil Payload, got %v", drained[0].Payload)
	}
}

// TestEmitWidget_NoEventHook_UpdatesAccumulator verifies that when no
// EventHook is set on the context, EmitWidget still appends the block to
// the accumulator (widget persists) and returns nil.
// Requirements: 4.1, 5.3
func TestEmitWidget_NoEventHook_UpdatesAccumulator(t *testing.T) {
	c := Background()
	// No EventHook set — c.EventHook() returns nil.

	acc := &widgetAccumulator{}
	c.Set(widgetAccumulatorKey{}, acc)

	payload := json.RawMessage(`{"value":42}`)
	block := WidgetBlock{Type: "progress", Payload: payload}

	if err := c.EmitWidget(block); err != nil {
		t.Fatalf("EmitWidget without EventHook returned unexpected error: %v", err)
	}

	drained := acc.drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 block in accumulator, got %d", len(drained))
	}
	if drained[0].Type != "progress" {
		t.Errorf("expected Type=%q, got %q", "progress", drained[0].Type)
	}
	if string(drained[0].Payload) != `{"value":42}` {
		t.Errorf("expected Payload=%q, got %q", `{"value":42}`, string(drained[0].Payload))
	}
}

// nonWidgetEmitterHook is an EventHook that does NOT implement WidgetEmitter.
// It embeds BaseEventHook for no-op implementations and tracks whether
// OnIterationStart was called (as a proxy for "was the hook invoked at all").
type nonWidgetEmitterHook struct {
	BaseEventHook
	iterationStartCalled bool
}

func (h *nonWidgetEmitterHook) OnIterationStart(_ *Context, _ int) {
	h.iterationStartCalled = true
}

// Compile-time assertion: nonWidgetEmitterHook implements EventHook but NOT WidgetEmitter.
var _ EventHook = (*nonWidgetEmitterHook)(nil)

// Compile-time assertion: nonWidgetEmitterHook does NOT implement WidgetEmitter.
// (Verified by the absence of an OnWidget method — no assertion needed.)

// Suppress unused import warning for time (used by BaseEventHook methods).
var _ = time.Duration(0)

// TestEmitWidget_HookNotWidgetEmitter_NoEvent_UpdatesAccumulator verifies
// that when the EventHook does not implement WidgetEmitter, EmitWidget is a
// no-op for event delivery but still updates the accumulator.
// Requirements: 4.1, 5.3
func TestEmitWidget_HookNotWidgetEmitter_NoEvent_UpdatesAccumulator(t *testing.T) {
	c := Background()

	hook := &nonWidgetEmitterHook{}
	c.WithEventHook(hook)

	acc := &widgetAccumulator{}
	c.Set(widgetAccumulatorKey{}, acc)

	block := WidgetBlock{Type: "table", Payload: json.RawMessage(`{"rows":3}`)}

	if err := c.EmitWidget(block); err != nil {
		t.Fatalf("EmitWidget returned unexpected error: %v", err)
	}

	// Accumulator must have the block.
	drained := acc.drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 block in accumulator, got %d", len(drained))
	}
	if drained[0].Type != "table" {
		t.Errorf("expected Type=%q, got %q", "table", drained[0].Type)
	}

	// OnIterationStart was never called — EmitWidget does not invoke EventHook
	// methods other than OnWidget (which this hook doesn't implement).
	if hook.iterationStartCalled {
		t.Error("expected hook methods not to be called by EmitWidget")
	}
}
