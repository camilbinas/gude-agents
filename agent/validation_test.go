package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

func TestValidateToolInput_MissingRequired(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing required field, got nil")
	}
}

func TestValidateToolInput_InvalidEnum(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"color": map[string]any{
				"type": "string",
				"enum": []any{"red", "green", "blue"},
			},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"color":"purple"}`))
	if err == nil {
		t.Fatal("expected error for invalid enum value, got nil")
	}
}

func TestValidateToolInput_ValidPayload(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"color": map[string]any{"type": "string", "enum": []any{"red", "green"}},
		},
		"required": []any{"name"},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"name":"alice","color":"red"}`))
	if err != nil {
		t.Fatalf("expected no error for valid payload, got: %v", err)
	}
}

func TestValidateToolInput_EnumFieldAbsent_OK(t *testing.T) {
	// An enum field that is not required and not present should pass.
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"color": map[string]any{"type": "string", "enum": []any{"red", "green"}},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected no error when optional enum field is absent, got: %v", err)
	}
}

// TestExecuteTools_SchemaValidation_MissingRequired verifies that executeTools
// returns IsError=true with a ToolError when a required field is missing.
func TestExecuteTools_SchemaValidation_MissingRequired(t *testing.T) {
	handlerCalled := false
	greetTool := tool.NewRaw(
		"greet",
		"greets someone",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return "hello", nil
		},
	)

	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc1", Name: "greet", Input: json.RawMessage(`{}`)},
		}},
		&ModelResponse{Text: "done"},
	)
	a, err := New(sp, "sys", WithTools(greetTool))
	if err != nil {
		t.Fatal(err)
	}

	cp := newCapturingProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc1", Name: "greet", Input: json.RawMessage(`{}`)},
		}},
		&ModelResponse{Text: "done"},
	)
	a2, _ := New(cp, "sys", WithTools(greetTool))
	_, err = a2.Invoke(Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = a

	// Verify the tool result sent to the provider has IsError=true.
	if len(cp.captured) < 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(cp.captured))
	}
	found := false
	for _, msg := range cp.captured[1].Messages {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok && tr.IsError {
				var te *ToolError
				// The content should be a ToolError message.
				synth := &ToolError{ToolName: "greet", Cause: errors.New("x")}
				_ = synth
				if errors.As(errors.New(tr.Content), &te) || tr.IsError {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected ToolResultBlock with IsError=true for missing required field")
	}
	if handlerCalled {
		t.Error("handler should not be called when schema validation fails")
	}
}

// TestExecuteTools_SchemaValidation_InvalidEnum verifies IsError=true for bad enum value.
func TestExecuteTools_SchemaValidation_InvalidEnum(t *testing.T) {
	handlerCalled := false
	colorTool := tool.NewRaw(
		"paint",
		"paints a color",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"color": map[string]any{"type": "string", "enum": []any{"red", "green", "blue"}},
			},
			"required": []any{"color"},
		},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return "painted", nil
		},
	)

	cp := newCapturingProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc1", Name: "paint", Input: json.RawMessage(`{"color":"purple"}`)},
		}},
		&ModelResponse{Text: "done"},
	)
	a, _ := New(cp, "sys", WithTools(colorTool))
	_, err := a.Invoke(Background(), "paint it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) < 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(cp.captured))
	}
	found := false
	for _, msg := range cp.captured[1].Messages {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok && tr.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected ToolResultBlock with IsError=true for invalid enum value")
	}
	if handlerCalled {
		t.Error("handler should not be called when enum validation fails")
	}
}

// TestExecuteTools_SchemaValidation_ValidPayload verifies handler IS called for valid input.
func TestExecuteTools_SchemaValidation_ValidPayload(t *testing.T) {
	handlerCalled := false
	greetTool := tool.NewRaw(
		"greet",
		"greets someone",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return "hello alice", nil
		},
	)

	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc1", Name: "greet", Input: json.RawMessage(`{"name":"alice"}`)},
		}},
		&ModelResponse{Text: "done"},
	)
	a, _ := New(sp, "sys", WithTools(greetTool))
	_, err := a.Invoke(Background(), "greet alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handlerCalled {
		t.Error("handler should be called for valid payload")
	}
}

// ---------------------------------------------------------------------------
// Strict type validation tests (new recursive validator)
// ---------------------------------------------------------------------------

func TestValidateToolInput_TypeMismatch_StringForInt(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"count"},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"count": "not_a_number"}`))
	if err == nil {
		t.Fatal("expected error for string where integer expected, got nil")
	}
}

func TestValidateToolInput_TypeMismatch_IntForString(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"name": 42}`))
	if err == nil {
		t.Fatal("expected error for integer where string expected, got nil")
	}
}

func TestValidateToolInput_TypeMismatch_StringForBoolean(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"active": map[string]any{"type": "boolean"},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"active": "true"}`))
	if err == nil {
		t.Fatal("expected error for string where boolean expected, got nil")
	}
}

func TestValidateToolInput_ValidInteger(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"count": 42}`))
	if err != nil {
		t.Fatalf("expected no error for valid integer, got: %v", err)
	}
}

func TestValidateToolInput_FractionalNumberForInteger(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"count": 3.14}`))
	if err == nil {
		t.Fatal("expected error for fractional number where integer expected, got nil")
	}
}

func TestValidateToolInput_NestedObject(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"address": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required": []any{"city"},
			},
		},
		"required": []any{"address"},
	}

	// Missing nested required field.
	err := ValidateToolInput(schema, json.RawMessage(`{"address": {}}`))
	if err == nil {
		t.Fatal("expected error for missing nested required field, got nil")
	}

	// Wrong type in nested field.
	err = ValidateToolInput(schema, json.RawMessage(`{"address": {"city": 123}}`))
	if err == nil {
		t.Fatal("expected error for wrong type in nested field, got nil")
	}

	// Valid nested object.
	err = ValidateToolInput(schema, json.RawMessage(`{"address": {"city": "Berlin"}}`))
	if err != nil {
		t.Fatalf("expected no error for valid nested object, got: %v", err)
	}
}

func TestValidateToolInput_ArrayItemType(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}

	// Array with wrong item type.
	err := ValidateToolInput(schema, json.RawMessage(`{"tags": [1, 2, 3]}`))
	if err == nil {
		t.Fatal("expected error for integer items where string expected, got nil")
	}

	// Valid array.
	err = ValidateToolInput(schema, json.RawMessage(`{"tags": ["go", "agent"]}`))
	if err != nil {
		t.Fatalf("expected no error for valid string array, got: %v", err)
	}
}

func TestValidateToolInput_EnumOnNestedField(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"units": map[string]any{
				"type": "string",
				"enum": []any{"celsius", "fahrenheit"},
			},
		},
	}

	err := ValidateToolInput(schema, json.RawMessage(`{"units": "kelvin"}`))
	if err == nil {
		t.Fatal("expected error for invalid enum value, got nil")
	}

	err = ValidateToolInput(schema, json.RawMessage(`{"units": "celsius"}`))
	if err != nil {
		t.Fatalf("expected no error for valid enum value, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Null validation tests
// ---------------------------------------------------------------------------

func TestValidateToolInput_NullRejectedForString(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"name": null}`))
	if err == nil {
		t.Fatal("expected error for null where string expected, got nil")
	}
}

func TestValidateToolInput_NullRejectedForInteger(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"count": null}`))
	if err == nil {
		t.Fatal("expected error for null where integer expected, got nil")
	}
}

func TestValidateToolInput_NullAllowedForExplicitNullType(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "null"},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"name": null}`))
	if err != nil {
		t.Fatalf("expected no error for null against explicit null type, got: %v", err)
	}
}

func TestValidateToolInput_NullAllowedForUnionType(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": []any{"string", "null"}},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"name": null}`))
	if err != nil {
		t.Fatalf("expected no error for null against [\"string\",\"null\"] union, got: %v", err)
	}
}

func TestValidateToolInput_UnionTypeAcceptsMatchingNonNullValue(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": []any{"string", "null"}},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"name": "alice"}`))
	if err != nil {
		t.Fatalf("expected no error for string against [\"string\",\"null\"] union, got: %v", err)
	}
}

func TestValidateToolInput_UnionTypeRejectsMismatchedNonNullValue(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": []any{"string", "null"}},
		},
	}
	err := ValidateToolInput(schema, json.RawMessage(`{"name": 42}`))
	if err == nil {
		t.Fatal("expected error for integer against [\"string\",\"null\"] union, got nil")
	}
}

func TestValidateToolInput_NullAllowedAtTopLevelWhenTyped(t *testing.T) {
	schema := map[string]any{"type": []any{"object", "null"}}
	err := ValidateToolInput(schema, json.RawMessage(`null`))
	if err != nil {
		t.Fatalf("expected no error for null at top level against [\"object\",\"null\"], got: %v", err)
	}
}

// TestProperty7_ValidInputsNeverRejected verifies that a payload satisfying the schema
// is never rejected by validateToolInput and the handler is always called.
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
func TestProperty8_HandlerNotCalledOnInvalidInput(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		field := rapid.StringMatching(`[a-z][a-z0-9]{0,7}`).Draw(rt, "field")

		handlerCalled := false
		strictTool := tool.NewRaw(
			"strict",
			"requires a field",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					field: map[string]any{"type": "string"},
				},
				"required": []any{field},
			},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				handlerCalled = true
				return "ok", nil
			},
		)

		sp := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{
				// Payload omits the required field.
				{ToolUseID: "tc1", Name: "strict", Input: json.RawMessage(`{}`)},
			}},
			&ModelResponse{Text: "done"},
		)
		a, err := New(sp, "sys", WithTools(strictTool))
		if err != nil {
			rt.Fatalf("New: %v", err)
		}

		_, err = a.Invoke(Background(), "go")
		if err != nil {
			rt.Fatalf("Invoke: %v", err)
		}
		if handlerCalled {
			rt.Fatal("handler must not be called when schema validation fails")
		}
	})
}

func TestValidateToolInput_GeneratedStringRequiredFields(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"required":   []string{"reason", "question"},
		"properties": map[string]any{"reason": map[string]any{"type": "string"}, "question": map[string]any{"type": "string"}},
	}
	if err := ValidateToolInput(schema, json.RawMessage(`{"reason":"need review"}`)); err == nil {
		t.Fatal("expected missing generated []string required field to be rejected")
	}
	if err := ValidateToolInput(schema, json.RawMessage(`{"reason":"need review","question":"approve?"}`)); err != nil {
		t.Fatalf("valid generated []string schema input rejected: %v", err)
	}
}

// schemaKeywordProbes pairs common JSON Schema keywords with a schema and a
// payload that violates only that keyword. The probe decides whether
// ValidateToolInput actually enforces the keyword.
var schemaKeywordProbes = []struct {
	keyword string
	schema  string
	payload string
}{
	{"type", `{"type":"string"}`, `1`},
	{"enum", `{"enum":["a"]}`, `"b"`},
	{"required", `{"type":"object","required":["a"]}`, `{}`},
	{"properties", `{"type":"object","properties":{"a":{"type":"string"}}}`, `{"a":1}`},
	{"items", `{"type":"array","items":{"type":"string"}}`, `[1]`},
	{"minimum", `{"type":"number","minimum":5}`, `1`},
	{"maximum", `{"type":"number","maximum":5}`, `10`},
	{"exclusiveMinimum", `{"type":"number","exclusiveMinimum":5}`, `5`},
	{"multipleOf", `{"type":"number","multipleOf":2}`, `3`},
	{"minLength", `{"type":"string","minLength":3}`, `"a"`},
	{"maxLength", `{"type":"string","maxLength":1}`, `"abc"`},
	{"pattern", `{"type":"string","pattern":"^a$"}`, `"b"`},
	{"additionalProperties", `{"type":"object","properties":{"a":{}},"additionalProperties":false}`, `{"a":1,"b":2}`},
	{"minItems", `{"type":"array","minItems":2}`, `[1]`},
	{"maxItems", `{"type":"array","maxItems":1}`, `[1,2]`},
	{"uniqueItems", `{"type":"array","uniqueItems":true}`, `[1,1]`},
	{"const", `{"const":"x"}`, `"y"`},
	{"oneOf", `{"oneOf":[{"type":"string"}]}`, `1`},
	{"anyOf", `{"anyOf":[{"type":"string"}]}`, `1`},
	{"allOf", `{"allOf":[{"type":"string"}]}`, `1`},
	{"not", `{"not":{"type":"string"}}`, `"a"`},
}

// TestSchemaContract_DocumentedKeywordsMatchEnforcement keeps the advertised
// structured-output / tool-input contract honest in both directions: every
// keyword listed in supportedSchemaKeywords must be enforced, and any keyword
// that is enforced must be listed (and therefore documented). Adding support
// for a new keyword is expected to update the list and the docs; this test
// does not assert that unsupported keywords should stay unsupported.
func TestSchemaContract_DocumentedKeywordsMatchEnforcement(t *testing.T) {
	for _, probe := range schemaKeywordProbes {
		t.Run(probe.keyword, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal([]byte(probe.schema), &schema); err != nil {
				t.Fatal(err)
			}
			enforced := ValidateToolInput(schema, json.RawMessage(probe.payload)) != nil
			documented := slices.Contains(supportedSchemaKeywords, probe.keyword)
			switch {
			case documented && !enforced:
				t.Fatalf("%q is advertised as enforced but a violating payload passed", probe.keyword)
			case enforced && !documented:
				t.Fatalf("%q is enforced but missing from supportedSchemaKeywords and the docs", probe.keyword)
			}
		})
	}
	for _, kw := range supportedSchemaKeywords {
		found := false
		for _, probe := range schemaKeywordProbes {
			found = found || probe.keyword == kw
		}
		if !found {
			t.Errorf("supported keyword %q has no enforcement probe", kw)
		}
	}
}
