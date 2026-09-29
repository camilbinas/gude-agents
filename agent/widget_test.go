package agent

import (
	"context"
	"encoding/json"
	"testing"
)

// toolCallContext builds a tool-call child Context outside the engine so
// EmitWidget can be exercised in isolation.
func toolCallContext(id string) (*Context, *toolCallRuntime) {
	rt := &toolCallRuntime{id: id, name: "t"}
	return Background().forInvocation(context.Background(), &invocationRuntime{}).forToolCall(rt), rt
}

// TestEmitWidget_NilPayload verifies that EmitWidget with a nil Payload
// succeeds and stores a WidgetBlock with nil Payload on the call.
func TestEmitWidget_NilPayload(t *testing.T) {
	c, rt := toolCallContext("c1")
	if err := EmitWidget(c, WidgetBlock{Type: "chart"}); err != nil {
		t.Fatalf("EmitWidget with nil Payload returned unexpected error: %v", err)
	}
	drained := rt.drainWidgets()
	if len(drained) != 1 || drained[0].Type != "chart" || drained[0].Payload != nil {
		t.Fatalf("drained = %#v", drained)
	}
}

// TestEmitWidget_WithoutStream_UpdatesCall verifies that without a stream
// consumer EmitWidget still records the block on the tool call.
func TestEmitWidget_WithoutStream_UpdatesCall(t *testing.T) {
	c, rt := toolCallContext("c1")
	if err := EmitWidget(c, WidgetBlock{Type: "progress", Payload: json.RawMessage(`{"value":42}`)}); err != nil {
		t.Fatal(err)
	}
	drained := rt.drainWidgets()
	if len(drained) != 1 || string(drained[0].Payload) != `{"value":42}` {
		t.Fatalf("drained = %#v", drained)
	}
}

// TestEmitWidget_InvalidBlock verifies that an empty Type is rejected and
// nothing is recorded.
func TestEmitWidget_InvalidBlock(t *testing.T) {
	c, rt := toolCallContext("c1")
	if err := EmitWidget(c, WidgetBlock{}); err == nil {
		t.Fatal("expected validation error")
	}
	if len(rt.drainWidgets()) != 0 {
		t.Fatal("invalid block must not be recorded")
	}
}

// TestEmitWidget_ThroughDerivedContext verifies that FromContext finds the
// tool-call Context through stdlib-derived contexts (e.g. middleware that
// wraps ctx with context.WithValue).
func TestEmitWidget_ThroughDerivedContext(t *testing.T) {
	type k struct{}
	c, rt := toolCallContext("c1")
	derived := context.WithValue(c, k{}, "v")
	if err := EmitWidget(derived, WidgetBlock{Type: "table"}); err != nil {
		t.Fatal(err)
	}
	if len(rt.drainWidgets()) != 1 {
		t.Fatal("widget not recorded through derived context")
	}
}
