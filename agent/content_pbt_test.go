package agent

import (
	"encoding/json"
	"testing"

	"pgregory.net/rapid"
)

// **Validates: Requirements 1.4, 4.3**

// TestProperty_WidgetBlockValidateRejectsEmptyType verifies Property 1:
// for any WidgetBlock with Type == "", Validate() must return non-nil,
// and for any WidgetBlock with a non-empty Type, Validate() must return nil.
func TestProperty_WidgetBlockValidateRejectsEmptyType(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate an arbitrary payload (nil or some JSON bytes).
		useNilPayload := rapid.Bool().Draw(t, "useNilPayload")
		var payload json.RawMessage
		if !useNilPayload {
			raw := rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, "payloadBytes")
			payload = json.RawMessage(raw)
		}

		// Property 1a: empty Type must be rejected.
		emptyBlock := WidgetBlock{Type: "", Payload: payload}
		if err := emptyBlock.Validate(); err == nil {
			t.Fatalf("Validate() returned nil for WidgetBlock with empty Type; want non-nil error")
		}

		// Property 1b: any non-empty Type must be accepted.
		nonEmptyType := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,30}`).Draw(t, "widgetType")
		validBlock := WidgetBlock{Type: nonEmptyType, Payload: payload}
		if err := validBlock.Validate(); err != nil {
			t.Fatalf("Validate() returned non-nil error %v for WidgetBlock with non-empty Type %q; want nil",
				err, nonEmptyType)
		}
	})
}
