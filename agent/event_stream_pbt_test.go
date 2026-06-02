package agent

import (
	"bytes"
	"encoding/json"
	"testing"

	"pgregory.net/rapid"
)

// widgetTypeGen generates a non-empty widget type string.
var widgetTypeGen = rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,30}`)

// widgetPayloadGen generates either a nil payload or arbitrary bytes as a payload.
func widgetPayloadGen(t *rapid.T, name string) json.RawMessage {
	useNil := rapid.Bool().Draw(t, name+"_useNil")
	if useNil {
		return nil
	}
	raw := rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, name+"_bytes")
	return json.RawMessage(raw)
}

// validWidgetBlockGen generates a WidgetBlock with a non-empty Type.
func validWidgetBlockGen(t *rapid.T) WidgetBlock {
	return WidgetBlock{
		Type:    widgetTypeGen.Draw(t, "widgetType"),
		Payload: widgetPayloadGen(t, "payload"),
	}
}

// TestProperty_EventWidgetDataFidelity verifies Property 3:
// for any valid WidgetBlock, the AgentEvent received on the channel must have
// WidgetType == block.Type and WidgetPayload byte-equal to block.Payload.
func TestProperty_EventWidgetDataFidelity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		block := validWidgetBlockGen(t)

		// Create a buffered channel and an eventStreamHook with no chained hook.
		ch := make(chan AgentEvent, 1)
		hook := &eventStreamHook{ch: ch, next: nil}

		// Call OnWidget — this is the method under test.
		hook.OnWidget(nil, block)

		// Read the event from the channel.
		event := <-ch

		// Assert Type is EventWidget.
		if event.Type != EventWidget {
			t.Fatalf("event.Type = %q, want %q", event.Type, EventWidget)
		}

		// Assert WidgetType equals block.Type.
		if event.WidgetType != block.Type {
			t.Fatalf("event.WidgetType = %q, want %q", event.WidgetType, block.Type)
		}

		// Assert WidgetPayload is byte-equal to block.Payload.
		if !bytes.Equal(event.WidgetPayload, block.Payload) {
			t.Fatalf("event.WidgetPayload = %v, want %v", event.WidgetPayload, block.Payload)
		}
	})
}

// TestProperty_OmitemptyOnNonWidgetEvents verifies Property 8:
// for any AgentEvent where WidgetType == "" and WidgetPayload == nil,
// marshaling to JSON must not contain "widget_type" or "widget_payload" substrings.
func TestProperty_OmitemptyOnNonWidgetEvents(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate an AgentEvent with no widget fields set.
		// We vary the Type to cover different event variants.
		eventTypes := []EventType{
			EventInvokeStart,
			EventIterationStart,
			EventModelStart,
			EventTextChunk,
			EventToolCallStart,
			EventToolCallEnd,
			EventModelEnd,
			EventIterationEnd,
			EventInvokeEnd,
			EventCustom,
		}
		idx := rapid.IntRange(0, len(eventTypes)-1).Draw(t, "eventTypeIdx")
		event := AgentEvent{
			Type:          eventTypes[idx],
			WidgetType:    "", // explicitly empty
			WidgetPayload: nil,
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		if bytes.Contains(data, []byte("widget_type")) {
			t.Fatalf("JSON output contains \"widget_type\" for non-widget event: %s", data)
		}
		if bytes.Contains(data, []byte("widget_payload")) {
			t.Fatalf("JSON output contains \"widget_payload\" for non-widget event: %s", data)
		}
	})
}

// mockWidgetEmitter is a test double that implements both EventHook and WidgetEmitter.
// It records the last OnWidget call for assertion.
type mockWidgetEmitter struct {
	BaseEventHook
	calledWith *WidgetBlock
}

func (m *mockWidgetEmitter) OnWidget(_ *Context, block WidgetBlock) {
	m.calledWith = &block
}

// TestProperty_FanInForwardingToChainedHook verifies Property 9:
// for any valid WidgetBlock, when eventStreamHook.next implements WidgetEmitter,
// OnWidget must be called on the chained hook with equal Type and Payload.
func TestProperty_FanInForwardingToChainedHook(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		block := validWidgetBlockGen(t)

		// Create a buffered channel and a mock chained hook.
		ch := make(chan AgentEvent, 1)
		mock := &mockWidgetEmitter{}
		hook := &eventStreamHook{ch: ch, next: mock}

		// Call OnWidget.
		hook.OnWidget(nil, block)

		// Drain the channel (we care about the forwarding, not the event itself).
		<-ch

		// Assert the chained hook was called.
		if mock.calledWith == nil {
			t.Fatalf("chained hook OnWidget was not called")
		}

		// Assert Type is equal.
		if mock.calledWith.Type != block.Type {
			t.Fatalf("chained hook received Type = %q, want %q", mock.calledWith.Type, block.Type)
		}

		// Assert Payload is byte-equal.
		if !bytes.Equal(mock.calledWith.Payload, block.Payload) {
			t.Fatalf("chained hook received Payload = %v, want %v", mock.calledWith.Payload, block.Payload)
		}
	})
}
