package conversation

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// ---------------------------------------------------------------------------
// jsonToContentBlock edge cases
// ---------------------------------------------------------------------------

// TestJSONRoundTrip_EmptyWidgetType verifies that a "widget" JSON block with
// an empty widget_type field deserializes to agent.WidgetBlock{Type: ""} —
// the zero value. This exercises the "widget" case in jsonToContentBlock.
// Requirements: 3.3, 1.4
func TestJSONRoundTrip_EmptyWidgetType(t *testing.T) {
	// Craft raw JSON that has type="widget" but no widget_type field.
	raw := []byte(`[{"role":"assistant","content":[{"type":"widget"}]}]`)

	msgs, err := UnmarshalMessages(raw)
	if err != nil {
		t.Fatalf("UnmarshalMessages returned unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msgs[0].Content))
	}

	wb, ok := msgs[0].Content[0].(agent.WidgetBlock)
	if !ok {
		t.Fatalf("expected agent.WidgetBlock, got %T", msgs[0].Content[0])
	}
	// The zero value: Type must be empty string, Payload must be nil.
	if wb.Type != "" {
		t.Errorf("expected empty Type (zero value), got %q", wb.Type)
	}
	if wb.Payload != nil {
		t.Errorf("expected nil Payload (zero value), got %v", wb.Payload)
	}
}

// TestJSONRoundTrip_UnknownType verifies that a content block with an
// unrecognized type falls through to the default branch in jsonToContentBlock
// and returns agent.TextBlock{} (empty text).
// Requirements: 3.3
func TestJSONRoundTrip_UnknownType(t *testing.T) {
	// Craft raw JSON with a type that doesn't match any known case.
	raw := []byte(`[{"role":"assistant","content":[{"type":"future_unknown_type","text":"ignored"}]}]`)

	msgs, err := UnmarshalMessages(raw)
	if err != nil {
		t.Fatalf("UnmarshalMessages returned unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msgs[0].Content))
	}

	tb, ok := msgs[0].Content[0].(agent.TextBlock)
	if !ok {
		t.Fatalf("expected agent.TextBlock (default branch), got %T", msgs[0].Content[0])
	}
	// The default branch returns agent.TextBlock{} — empty text.
	if tb.Text != "" {
		t.Errorf("expected empty Text from default branch, got %q", tb.Text)
	}
}
