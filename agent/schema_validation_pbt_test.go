package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// Feature: agent-framework-improvements, Property 7: Valid tool inputs are never rejected by schema validation
// Feature: agent-framework-improvements, Property 8: Invalid tool inputs are always rejected without calling the handler

// TestProperty7_ValidInputsNeverRejected verifies that a payload satisfying the schema
// is never rejected by validateToolInput and the handler is always called.
//
// Validates: Requirements 13.3, 13.4
func TestProperty7_ValidInputsNeverRejected(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate 1–4 required field names.
		numFields := rapid.IntRange(1, 4).Draw(rt, "numFields")
		fields := make([]string, numFields)
		for i := range numFields {
			fields[i] = rapid.StringMatching(`[a-z][a-z0-9]{0,7}`).Draw(rt, "field")
		}
		// Deduplicate.
		seen := map[string]bool{}
		unique := fields[:0]
		for _, f := range fields {
			if !seen[f] {
				seen[f] = true
				unique = append(unique, f)
			}
		}
		fields = unique

		// Build schema with all fields required.
		props := map[string]any{}
		required := make([]any, len(fields))
		for i, f := range fields {
			props[f] = map[string]any{"type": "string"}
			required[i] = f
		}
		schema := map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		}

		// Build a conforming payload.
		payload := map[string]any{}
		for _, f := range fields {
			payload[f] = "value"
		}
		inputBytes, _ := json.Marshal(payload)

		err := ValidateToolInput(schema, json.RawMessage(inputBytes))
		if err != nil {
			rt.Fatalf("valid payload rejected: %v (fields=%v)", err, fields)
		}
	})
}

// TestProperty7_ValidEnumInputsNeverRejected verifies that enum-constrained fields
// with valid values are never rejected.
//
// Validates: Requirements 13.3, 13.4
func TestProperty7_ValidEnumInputsNeverRejected(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate 2–4 enum values.
		numVals := rapid.IntRange(2, 4).Draw(rt, "numVals")
		enumVals := make([]any, numVals)
		for i := range numVals {
			enumVals[i] = rapid.StringMatching(`[a-z]{2,6}`).Draw(rt, "enumVal")
		}

		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"choice": map[string]any{"type": "string", "enum": enumVals},
			},
			"required": []any{"choice"},
		}

		// Pick a valid value from the enum.
		idx := rapid.IntRange(0, numVals-1).Draw(rt, "idx")
		payload := map[string]any{"choice": enumVals[idx]}
		inputBytes, _ := json.Marshal(payload)

		err := ValidateToolInput(schema, json.RawMessage(inputBytes))
		if err != nil {
			rt.Fatalf("valid enum value %v rejected: %v", enumVals[idx], err)
		}
	})
}

// TestProperty8_InvalidInputsAlwaysRejected verifies that a payload violating the schema
// is always rejected and the handler is never called.
//
// Validates: Requirements 13.1, 13.2
func TestProperty8_InvalidInputsAlwaysRejected(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a required field name.
		field := rapid.StringMatching(`[a-z][a-z0-9]{0,7}`).Draw(rt, "field")

		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				field: map[string]any{"type": "string"},
			},
			"required": []any{field},
		}

		// Payload deliberately omits the required field.
		err := ValidateToolInput(schema, json.RawMessage(`{}`))
		if err == nil {
			rt.Fatalf("expected rejection for missing required field %q, got nil", field)
		}
	})
}

// TestProperty8_InvalidEnumAlwaysRejected verifies that an out-of-enum value is always rejected.
//
// Validates: Requirements 13.1, 13.2
func TestProperty8_InvalidEnumAlwaysRejected(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Fixed enum so we can guarantee the payload value is outside it.
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"color": map[string]any{
					"type": "string",
					"enum": []any{"red", "green", "blue"},
				},
			},
			"required": []any{"color"},
		}

		// Generate a value that is NOT in the enum.
		badVal := rapid.StringMatching(`[a-z]{4,10}`).
			Filter(func(s string) bool {
				return s != "red" && s != "green" && s != "blue"
			}).
			Draw(rt, "badVal")

		payload := map[string]any{"color": badVal}
		inputBytes, _ := json.Marshal(payload)

		err := ValidateToolInput(schema, json.RawMessage(inputBytes))
		if err == nil {
			rt.Fatalf("expected rejection for out-of-enum value %q, got nil", badVal)
		}
	})
}

// TestProperty8_HandlerNotCalledOnInvalidInput verifies end-to-end that the handler
// is never invoked when schema validation fails.
//
// Validates: Requirements 13.1, 13.2
func TestProperty8_HandlerNotCalledOnInvalidInput(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		field := rapid.StringMatching(`[a-z][a-z0-9]{0,7}`).Draw(rt, "field")

		handlerCalled := false
		strictTool := tool.NewRaw("strict", "requires a field", map[string]any{
			"type": "object",
			"properties": map[string]any{
				field: map[string]any{"type": "string"},
			},
			"required": []any{field},
		}, func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return "ok", nil
		})

		sp := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{
				// Payload omits the required field.
				{ToolUseID: "tc1", Name: "strict", Input: json.RawMessage(`{}`)},
			}},
			&ProviderResponse{Text: "done"},
		)
		a, err := New(sp, prompt.Text("sys"), []tool.Tool{strictTool})
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		_, _, err = a.Invoke(context.Background(), "go")
		if err != nil {
			rt.Fatalf("Invoke: %v", err)
		}
		if handlerCalled {
			rt.Fatal("handler must not be called when schema validation fails")
		}
	})
}
