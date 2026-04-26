package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// structuredTestProvider captures the ConverseParams passed to Converse and returns
// a configurable response. Used specifically for structured output tests.
type structuredTestProvider struct {
	capturedParams ConverseParams
	response       *ProviderResponse
	err            error
}

func (p *structuredTestProvider) Converse(_ context.Context, params ConverseParams) (*ProviderResponse, error) {
	p.capturedParams = params
	if p.err != nil {
		return nil, p.err
	}
	return p.response, nil
}

func (p *structuredTestProvider) ConverseStream(_ context.Context, params ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	return p.Converse(context.Background(), params)
}

// ---------------------------------------------------------------------------
// Task 8.2 — Property 2: InvokeStructured sets up ConverseParams correctly
// Feature: agent-framework-v2, Property 2: InvokeStructured sets up ConverseParams correctly
// **Validates: Requirements 2.2, 2.3**
// ---------------------------------------------------------------------------

type SimpleStruct struct {
	Name  string `json:"name" description:"A name" required:"true"`
	Count int    `json:"count" description:"A count"`
}

func TestProperty2_InvokeStructured_ConverseParams(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		userMsg := rapid.String().Draw(t, "userMessage")
		systemPrompt := rapid.String().Draw(t, "systemPrompt")

		// Provider returns a valid tool call so InvokeStructured doesn't error on response parsing.
		validJSON, _ := json.Marshal(SimpleStruct{Name: "test", Count: 1})
		sp := &structuredTestProvider{
			response: &ProviderResponse{
				ToolCalls: []tool.Call{
					{ToolUseID: "tc1", Name: structuredOutputToolName, Input: json.RawMessage(validJSON)},
				},
			},
		}

		a, err := New(sp, prompt.Text(systemPrompt), nil)
		if err != nil {
			t.Fatal(err)
		}

		_, _, err = InvokeStructured[SimpleStruct](context.Background(), a, userMsg)
		if err != nil {
			t.Fatal(err)
		}

		params := sp.capturedParams

		// Verify system prompt is passed through.
		if params.System != systemPrompt {
			t.Fatalf("expected system=%q, got %q", systemPrompt, params.System)
		}

		// Verify user message is in messages.
		if len(params.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(params.Messages))
		}
		if params.Messages[0].Role != RoleUser {
			t.Fatalf("expected role=user, got %s", params.Messages[0].Role)
		}

		// Verify ToolConfig has exactly one tool spec with the response tool name.
		if len(params.ToolConfig) != 1 {
			t.Fatalf("expected 1 tool spec, got %d", len(params.ToolConfig))
		}
		toolSpec := params.ToolConfig[0]
		if toolSpec.Name != structuredOutputToolName {
			t.Fatalf("expected tool name=%q, got %q", structuredOutputToolName, toolSpec.Name)
		}

		// Verify the tool spec's InputSchema matches tool.GenerateSchema[SimpleStruct]().
		expectedSchema := tool.GenerateSchema[SimpleStruct]()
		if !reflect.DeepEqual(toolSpec.InputSchema, expectedSchema) {
			t.Fatalf("tool spec InputSchema mismatch:\n  got:  %v\n  want: %v", toolSpec.InputSchema, expectedSchema)
		}

		// Verify ToolChoice is set correctly.
		if params.ToolChoice == nil {
			t.Fatal("expected ToolChoice to be set")
		}
		if params.ToolChoice.Mode != tool.ChoiceTool {
			t.Fatalf("expected ToolChoice.Mode=%q, got %q", tool.ChoiceTool, params.ToolChoice.Mode)
		}
		if params.ToolChoice.Name != structuredOutputToolName {
			t.Fatalf("expected ToolChoice.Name=%q, got %q", structuredOutputToolName, params.ToolChoice.Name)
		}
	})
}

// ---------------------------------------------------------------------------
// Task 8.3 — Property 3: InvokeStructured deserialization round-trip
// Feature: agent-framework-v2, Property 3: InvokeStructured deserialization round-trip
// **Validates: Requirements 2.4**
// ---------------------------------------------------------------------------

type RoundTripStruct struct {
	Title   string   `json:"title"`
	Value   int      `json:"value"`
	Active  bool     `json:"active"`
	Score   float64  `json:"score"`
	Tags    []string `json:"tags"`
	Numbers []int    `json:"numbers"`
}

func TestProperty3_InvokeStructured_DeserializationRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random RoundTripStruct instance.
		original := RoundTripStruct{
			Title:   rapid.String().Draw(t, "title"),
			Value:   rapid.Int().Draw(t, "value"),
			Active:  rapid.Bool().Draw(t, "active"),
			Score:   rapid.Float64().Draw(t, "score"),
			Tags:    rapid.SliceOf(rapid.String()).Draw(t, "tags"),
			Numbers: rapid.SliceOf(rapid.Int()).Draw(t, "numbers"),
		}

		// Serialize the original to JSON (simulating what the LLM would return).
		inputJSON, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("failed to marshal original: %v", err)
		}

		// Mock provider returns the serialized struct as a tool call.
		sp := &structuredTestProvider{
			response: &ProviderResponse{
				ToolCalls: []tool.Call{
					{ToolUseID: "tc1", Name: structuredOutputToolName, Input: json.RawMessage(inputJSON)},
				},
				Usage: TokenUsage{InputTokens: 10, OutputTokens: 5},
			},
		}

		a, err := New(sp, prompt.Text("sys"), nil)
		if err != nil {
			t.Fatal(err)
		}

		result, usage, err := InvokeStructured[RoundTripStruct](context.Background(), a, "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the deserialized result matches the original.
		if result.Title != original.Title {
			t.Errorf("Title: got %q, want %q", result.Title, original.Title)
		}
		if result.Value != original.Value {
			t.Errorf("Value: got %d, want %d", result.Value, original.Value)
		}
		if result.Active != original.Active {
			t.Errorf("Active: got %v, want %v", result.Active, original.Active)
		}
		// For float64, use JSON round-trip comparison (NaN, Inf handled by marshal).
		origJSON, _ := json.Marshal(original.Score)
		resultJSON, _ := json.Marshal(result.Score)
		if string(origJSON) != string(resultJSON) {
			t.Errorf("Score: got %s, want %s", resultJSON, origJSON)
		}

		// Compare tags — nil vs empty slice normalization.
		if len(original.Tags) == 0 && len(result.Tags) == 0 {
			// Both empty, ok.
		} else if !reflect.DeepEqual(result.Tags, original.Tags) {
			t.Errorf("Tags: got %v, want %v", result.Tags, original.Tags)
		}

		if len(original.Numbers) == 0 && len(result.Numbers) == 0 {
			// Both empty, ok.
		} else if !reflect.DeepEqual(result.Numbers, original.Numbers) {
			t.Errorf("Numbers: got %v, want %v", result.Numbers, original.Numbers)
		}

		// Verify usage is passed through.
		if usage.InputTokens != 10 || usage.OutputTokens != 5 {
			t.Errorf("Usage: got %+v, want {10, 5}", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// Task 8.4 — Unit tests for InvokeStructured error cases
// Requirements: 2.5, 2.6
// ---------------------------------------------------------------------------

func TestInvokeStructured_NoToolCallReturned(t *testing.T) {
	// Provider returns a text-only response with no tool calls.
	sp := &structuredTestProvider{
		response: &ProviderResponse{Text: "I can't do that"},
	}

	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = InvokeStructured[SimpleStruct](context.Background(), a, "test")
	if err == nil {
		t.Fatal("expected error for no tool call, got nil")
	}

	expected := fmt.Sprintf("structured output: LLM did not return a tool call to %s", structuredOutputToolName)
	if err.Error() != expected {
		t.Errorf("error mismatch:\n  got:  %q\n  want: %q", err.Error(), expected)
	}
}

func TestInvokeStructured_WrongToolName(t *testing.T) {
	// Provider returns a tool call with the wrong name.
	sp := &structuredTestProvider{
		response: &ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc1", Name: "wrong_tool", Input: json.RawMessage(`{}`)},
			},
		},
	}

	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = InvokeStructured[SimpleStruct](context.Background(), a, "test")
	if err == nil {
		t.Fatal("expected error for wrong tool name, got nil")
	}

	expected := fmt.Sprintf("structured output: LLM called tool %q instead of %s", "wrong_tool", structuredOutputToolName)
	if err.Error() != expected {
		t.Errorf("error mismatch:\n  got:  %q\n  want: %q", err.Error(), expected)
	}
}

func TestInvokeStructured_MalformedJSON(t *testing.T) {
	// Provider returns a tool call with invalid JSON.
	sp := &structuredTestProvider{
		response: &ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc1", Name: structuredOutputToolName, Input: json.RawMessage(`{not valid json}`)},
			},
		},
	}

	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = InvokeStructured[SimpleStruct](context.Background(), a, "test")
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}

	if got := err.Error(); len(got) < 20 {
		t.Errorf("expected descriptive error, got: %q", got)
	}
	// Should contain the wrapping prefix.
	if got := err.Error(); !contains(got, "structured output: failed to deserialize response:") {
		t.Errorf("expected error to contain deserialization prefix, got: %q", got)
	}
}

func TestInvokeStructured_ProviderError(t *testing.T) {
	// Provider returns an error.
	sp := &structuredTestProvider{
		err: fmt.Errorf("connection refused"),
	}

	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = InvokeStructured[SimpleStruct](context.Background(), a, "test")
	if err == nil {
		t.Fatal("expected error for provider failure, got nil")
	}

	if got := err.Error(); !contains(got, "provider error:") || !contains(got, "connection refused") {
		t.Errorf("expected wrapped provider error, got: %q", got)
	}
}

// contains is a helper to check substring presence.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Task 8.5 — Property 1: Schema generation preserves struct metadata
// Feature: agent-framework-v2, Property 1: Schema generation preserves struct metadata
// **Validates: Requirements 1.1, 1.2, 1.3, 1.4, 1.5**
// ---------------------------------------------------------------------------

// We test schema generation with a variety of struct configurations using rapid.
// Since Go generics require compile-time types, we test with a fixed struct that
// exercises all tag features, and use rapid to generate random field values to
// ensure the schema is always consistent regardless of the struct's zero/non-zero state.

type FullTagStruct struct {
	Name     string   `json:"name" description:"The name" required:"true"`
	Status   string   `json:"status" description:"Current status" enum:"active,inactive,pending" required:"true"`
	Age      int      `json:"age" description:"Age in years"`
	Score    float64  `json:"score" description:"A score value"`
	Active   bool     `json:"active" description:"Whether active"`
	Tags     []string `json:"tags" description:"List of tags"`
	Priority string   `json:"priority" enum:"low,medium,high"`
}

func TestProperty1_SchemaGenerationPreservesStructMetadata(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// The schema is generated from the type, not from values, so it should
		// always be the same. We run this many times to confirm stability.
		schema := tool.GenerateSchema[FullTagStruct]()

		// Requirement 1.1: produces a valid JSON Schema object
		if schema["type"] != "object" {
			t.Fatalf("expected type=object, got %v", schema["type"])
		}

		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatal("expected properties to be map[string]any")
		}

		// Requirement 1.2: json tag values used as property names
		expectedFields := []string{"name", "status", "age", "score", "active", "tags", "priority"}
		for _, f := range expectedFields {
			if _, ok := props[f]; !ok {
				t.Fatalf("expected property %q from json tag", f)
			}
		}

		// Requirement 1.3: description tags preserved
		descChecks := map[string]string{
			"name":   "The name",
			"status": "Current status",
			"age":    "Age in years",
			"score":  "A score value",
			"active": "Whether active",
			"tags":   "List of tags",
		}
		for field, wantDesc := range descChecks {
			p := props[field].(map[string]any)
			if got, ok := p["description"]; !ok || got != wantDesc {
				t.Fatalf("field %q: expected description=%q, got %v", field, wantDesc, got)
			}
		}

		// Requirement 1.4: required:"true" fields appear in required array
		reqSlice, ok := schema["required"].([]string)
		if !ok {
			t.Fatal("expected required to be []string")
		}
		reqSet := map[string]bool{}
		for _, r := range reqSlice {
			reqSet[r] = true
		}
		if !reqSet["name"] {
			t.Fatal("expected 'name' in required array")
		}
		if !reqSet["status"] {
			t.Fatal("expected 'status' in required array")
		}
		// Fields without required:"true" should NOT be in required.
		if reqSet["age"] {
			t.Fatal("'age' should not be in required array")
		}

		// Requirement 1.5: enum tag values appear as enum constraints
		statusProp := props["status"].(map[string]any)
		statusEnum, ok := statusProp["enum"].([]any)
		if !ok {
			t.Fatal("expected status to have enum constraint")
		}
		expectedEnum := []any{"active", "inactive", "pending"}
		if !reflect.DeepEqual(statusEnum, expectedEnum) {
			t.Fatalf("status enum: got %v, want %v", statusEnum, expectedEnum)
		}

		priorityProp := props["priority"].(map[string]any)
		priorityEnum, ok := priorityProp["enum"].([]any)
		if !ok {
			t.Fatal("expected priority to have enum constraint")
		}
		expectedPriorityEnum := []any{"low", "medium", "high"}
		if !reflect.DeepEqual(priorityEnum, expectedPriorityEnum) {
			t.Fatalf("priority enum: got %v, want %v", priorityEnum, expectedPriorityEnum)
		}

		// Verify no enum on fields without enum tag.
		nameProp := props["name"].(map[string]any)
		if _, hasEnum := nameProp["enum"]; hasEnum {
			t.Fatal("'name' should not have enum constraint")
		}
	})
}

// TestProperty1_SchemaGeneration_NestedStruct verifies schema generation handles nested structs.
func TestProperty1_SchemaGeneration_NestedStruct(t *testing.T) {
	type Inner struct {
		Value string `json:"value" description:"Inner value" required:"true"`
	}
	type Outer struct {
		Label string `json:"label" description:"Outer label" required:"true"`
		Inner Inner  `json:"inner" description:"Nested object"`
	}

	rapid.Check(t, func(t *rapid.T) {
		schema := tool.GenerateSchema[Outer]()

		props := schema["properties"].(map[string]any)

		// Outer field
		labelProp := props["label"].(map[string]any)
		if labelProp["type"] != "string" {
			t.Fatalf("label type: got %v, want string", labelProp["type"])
		}

		// Nested struct
		innerProp := props["inner"].(map[string]any)
		if innerProp["type"] != "object" {
			t.Fatalf("inner type: got %v, want object", innerProp["type"])
		}

		innerProps := innerProp["properties"].(map[string]any)
		valueProp := innerProps["value"].(map[string]any)
		if valueProp["type"] != "string" {
			t.Fatalf("inner.value type: got %v, want string", valueProp["type"])
		}
		if valueProp["description"] != "Inner value" {
			t.Fatalf("inner.value description: got %v, want 'Inner value'", valueProp["description"])
		}
	})
}

// TestProperty1_SchemaGeneration_SliceFields verifies schema generation handles slice fields.
func TestProperty1_SchemaGeneration_SliceFields(t *testing.T) {
	type WithSlices struct {
		Names  []string `json:"names" description:"List of names"`
		Scores []int    `json:"scores" description:"List of scores"`
	}

	rapid.Check(t, func(t *rapid.T) {
		schema := tool.GenerateSchema[WithSlices]()
		props := schema["properties"].(map[string]any)

		namesProp := props["names"].(map[string]any)
		if namesProp["type"] != "array" {
			t.Fatalf("names type: got %v, want array", namesProp["type"])
		}
		namesItems := namesProp["items"].(map[string]any)
		if namesItems["type"] != "string" {
			t.Fatalf("names items type: got %v, want string", namesItems["type"])
		}

		scoresProp := props["scores"].(map[string]any)
		if scoresProp["type"] != "array" {
			t.Fatalf("scores type: got %v, want array", scoresProp["type"])
		}
		scoresItems := scoresProp["items"].(map[string]any)
		if scoresItems["type"] != "integer" {
			t.Fatalf("scores items type: got %v, want integer", scoresItems["type"])
		}
	})
}

// ---------------------------------------------------------------------------
// Task 11.1 — Unit tests for InvokeStructured lifecycle hooks
// ---------------------------------------------------------------------------

// trackingMemory records Load/Save calls for assertion.
type trackingMemory struct {
	loadCalled int
	saveCalled int
	data       []Message
}

func (m *trackingMemory) Load(_ context.Context, _ string) ([]Message, error) {
	m.loadCalled++
	return m.data, nil
}

func (m *trackingMemory) Save(_ context.Context, _ string, msgs []Message) error {
	m.saveCalled++
	m.data = msgs
	return nil
}

func TestInvokeStructured_MemoryLoadAndSaveCalled(t *testing.T) {
	validJSON, _ := json.Marshal(SimpleStruct{Name: "test", Count: 1})
	sp := &structuredTestProvider{
		response: &ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc1", Name: structuredOutputToolName, Input: json.RawMessage(validJSON)},
			},
		},
	}

	mem := &trackingMemory{}
	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(mem, "conv1"))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = InvokeStructured[SimpleStruct](context.Background(), a, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mem.loadCalled != 1 {
		t.Errorf("expected Load called once, got %d", mem.loadCalled)
	}
	if mem.saveCalled != 1 {
		t.Errorf("expected Save called once, got %d", mem.saveCalled)
	}
}

func TestInvokeStructured_InputGuardrailApplied(t *testing.T) {
	var receivedMsg string
	validJSON, _ := json.Marshal(SimpleStruct{Name: "test", Count: 1})
	sp := &structuredTestProvider{
		response: &ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc1", Name: structuredOutputToolName, Input: json.RawMessage(validJSON)},
			},
		},
	}

	a, err := New(sp, prompt.Text("sys"), nil,
		WithInputGuardrail(func(_ context.Context, msg string) (string, error) {
			receivedMsg = msg
			return msg + " [filtered]", nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = InvokeStructured[SimpleStruct](context.Background(), a, "original")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMsg != "original" {
		t.Errorf("expected guardrail to receive %q, got %q", "original", receivedMsg)
	}
	// The provider should have received the filtered message.
	if len(sp.capturedParams.Messages) == 0 {
		t.Fatal("no messages captured")
	}
	tb, ok := sp.capturedParams.Messages[len(sp.capturedParams.Messages)-1].Content[0].(TextBlock)
	if !ok || tb.Text != "original [filtered]" {
		t.Errorf("expected provider to receive filtered message, got %+v", sp.capturedParams.Messages)
	}
}

func TestInvokeStructured_InputGuardrailError(t *testing.T) {
	sp := &structuredTestProvider{}
	cause := fmt.Errorf("blocked")
	a, err := New(sp, prompt.Text("sys"), nil,
		WithInputGuardrail(func(_ context.Context, msg string) (string, error) {
			return "", cause
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, invokeErr := InvokeStructured[SimpleStruct](context.Background(), a, "bad input")
	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}
	var ge *GuardrailError
	if !errors.As(invokeErr, &ge) || ge.Direction != "input" {
		t.Errorf("expected input GuardrailError, got %T: %v", invokeErr, invokeErr)
	}
}

func TestInvokeStructured_OutputGuardrailApplied(t *testing.T) {
	original := SimpleStruct{Name: "alice", Count: 5}
	validJSON, _ := json.Marshal(original)
	sp := &structuredTestProvider{
		response: &ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc1", Name: structuredOutputToolName, Input: json.RawMessage(validJSON)},
			},
		},
	}

	// Output guardrail replaces "alice" with "bob" in the raw JSON.
	a, err := New(sp, prompt.Text("sys"), nil,
		WithOutputGuardrail(func(_ context.Context, text string) (string, error) {
			return `{"name":"bob","count":5}`, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := InvokeStructured[SimpleStruct](context.Background(), a, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "bob" {
		t.Errorf("expected output guardrail to transform result, got Name=%q", result.Name)
	}
}

func TestInvokeStructured_OutputGuardrailError(t *testing.T) {
	validJSON, _ := json.Marshal(SimpleStruct{Name: "test", Count: 1})
	sp := &structuredTestProvider{
		response: &ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc1", Name: structuredOutputToolName, Input: json.RawMessage(validJSON)},
			},
		},
	}
	cause := fmt.Errorf("output blocked")
	a, err := New(sp, prompt.Text("sys"), nil,
		WithOutputGuardrail(func(_ context.Context, text string) (string, error) {
			return "", cause
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, invokeErr := InvokeStructured[SimpleStruct](context.Background(), a, "hello")
	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}
	var ge *GuardrailError
	if !errors.As(invokeErr, &ge) || ge.Direction != "output" {
		t.Errorf("expected output GuardrailError, got %T: %v", invokeErr, invokeErr)
	}
}

// ---------------------------------------------------------------------------
// Task 11.2 — Property tests for guardrail ordering (Property 9 & 10)
// ---------------------------------------------------------------------------

// Feature: agent-framework-improvements, Property 9: InvokeStructured applies all input guardrails in order
// Validates: Requirements 6.1
func TestProperty9_InvokeStructured_InputGuardrailOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 6).Draw(rt, "n")

		validJSON, _ := json.Marshal(SimpleStruct{Name: "x", Count: 0})
		sp := &structuredTestProvider{
			response: &ProviderResponse{
				ToolCalls: []tool.Call{
					{ToolUseID: "tc1", Name: structuredOutputToolName, Input: json.RawMessage(validJSON)},
				},
			},
		}

		// Build n guardrails, each appending a unique marker "|i".
		guardrails := make([]InputGuardrail, n)
		for i := range n {
			idx := i
			guardrails[i] = func(_ context.Context, msg string) (string, error) {
				return fmt.Sprintf("%s|%d", msg, idx), nil
			}
		}

		opts := make([]Option, n)
		for i, g := range guardrails {
			opts[i] = WithInputGuardrail(g)
		}

		a, err := New(sp, prompt.Text("sys"), nil, opts...)
		if err != nil {
			rt.Fatal(err)
		}

		_, _, err = InvokeStructured[SimpleStruct](context.Background(), a, "start")
		if err != nil {
			rt.Fatalf("unexpected error: %v", err)
		}

		// Build expected message: "start|0|1|...|n-1"
		expected := "start"
		for i := range n {
			expected = fmt.Sprintf("%s|%d", expected, i)
		}

		if len(sp.capturedParams.Messages) == 0 {
			rt.Fatal("no messages captured")
		}
		lastMsg := sp.capturedParams.Messages[len(sp.capturedParams.Messages)-1]
		tb, ok := lastMsg.Content[0].(TextBlock)
		if !ok || tb.Text != expected {
			rt.Fatalf("expected message %q after %d guardrails, got %q", expected, n, tb.Text)
		}
	})
}

// Feature: agent-framework-improvements, Property 10: InvokeStructured applies all output guardrails in order
// Validates: Requirements 6.2
func TestProperty10_InvokeStructured_OutputGuardrailOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 6).Draw(rt, "n")

		// Start with valid JSON; each guardrail appends a marker to the raw text,
		// so the final result won't be valid JSON — we just check the chain, not deserialization.
		// Use a string type so any text is valid.
		sp := &structuredTestProvider{
			response: &ProviderResponse{
				ToolCalls: []tool.Call{
					{ToolUseID: "tc1", Name: structuredOutputToolName, Input: json.RawMessage(`"start"`)},
				},
			},
		}

		guardrails := make([]OutputGuardrail, n)
		for i := range n {
			idx := i
			guardrails[i] = func(_ context.Context, text string) (string, error) {
				return fmt.Sprintf("%s|%d", text, idx), nil
			}
		}

		opts := make([]Option, n)
		for i, g := range guardrails {
			opts[i] = WithOutputGuardrail(g)
		}

		a, err := New(sp, prompt.Text("sys"), nil, opts...)
		if err != nil {
			rt.Fatal(err)
		}
		_ = a

		// InvokeStructured[string] — raw JSON string "start" deserializes to Go string "start".
		// After n guardrails the raw text becomes `"start"|0|1|...|n-1` which won't unmarshal,
		// so we just verify the GuardrailError is NOT returned (all guardrails ran without error)
		// and that the final text passed to json.Unmarshal contains all markers.
		// To make this testable without a deserialization error, capture the text via a final
		// guardrail that records it and returns the original valid JSON.
		var capturedFinal string
		recordGuardrail := WithOutputGuardrail(func(_ context.Context, text string) (string, error) {
			capturedFinal = text
			return `"done"`, nil // return valid JSON so deserialization succeeds
		})

		a2, err := New(sp, prompt.Text("sys"), nil, append(opts, recordGuardrail)...)
		if err != nil {
			rt.Fatal(err)
		}

		_, _, err = InvokeStructured[string](context.Background(), a2, "hello")
		if err != nil {
			rt.Fatalf("unexpected error: %v", err)
		}

		// capturedFinal should be `"start"|0|1|...|n-1`
		expected := `"start"`
		for i := range n {
			expected = fmt.Sprintf("%s|%d", expected, i)
		}
		if capturedFinal != expected {
			rt.Fatalf("expected output chain %q, got %q", expected, capturedFinal)
		}
	})
}
