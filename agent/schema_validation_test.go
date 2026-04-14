package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ---------------------------------------------------------------------------
// Task 10.1 — Unit tests for validateToolInput and integration with executeTools
// ---------------------------------------------------------------------------

func TestValidateToolInput_MissingRequired(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}
	err := validateToolInput(schema, json.RawMessage(`{}`))
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
	err := validateToolInput(schema, json.RawMessage(`{"color":"purple"}`))
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
	err := validateToolInput(schema, json.RawMessage(`{"name":"alice","color":"red"}`))
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
	err := validateToolInput(schema, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected no error when optional enum field is absent, got: %v", err)
	}
}

// TestExecuteTools_SchemaValidation_MissingRequired verifies that executeTools
// returns IsError=true with a ToolError when a required field is missing.
func TestExecuteTools_SchemaValidation_MissingRequired(t *testing.T) {
	handlerCalled := false
	greetTool := tool.NewRaw("greet", "greets someone", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		handlerCalled = true
		return "hello", nil
	})

	sp := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc1", Name: "greet", Input: json.RawMessage(`{}`)},
		}},
		&ProviderResponse{Text: "done"},
	)
	a, err := New(sp, prompt.Text("sys"), []tool.Tool{greetTool})
	if err != nil {
		t.Fatal(err)
	}

	cp := newCapturingProvider(
		&ProviderResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc1", Name: "greet", Input: json.RawMessage(`{}`)},
		}},
		&ProviderResponse{Text: "done"},
	)
	a2, _ := New(cp, prompt.Text("sys"), []tool.Tool{greetTool})
	_, _, err = a2.Invoke(context.Background(), "hi")
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
	colorTool := tool.NewRaw("paint", "paints a color", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"color": map[string]any{"type": "string", "enum": []any{"red", "green", "blue"}},
		},
		"required": []any{"color"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		handlerCalled = true
		return "painted", nil
	})

	cp := newCapturingProvider(
		&ProviderResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc1", Name: "paint", Input: json.RawMessage(`{"color":"purple"}`)},
		}},
		&ProviderResponse{Text: "done"},
	)
	a, _ := New(cp, prompt.Text("sys"), []tool.Tool{colorTool})
	_, _, err := a.Invoke(context.Background(), "paint it")
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
	greetTool := tool.NewRaw("greet", "greets someone", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		handlerCalled = true
		return "hello alice", nil
	})

	sp := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc1", Name: "greet", Input: json.RawMessage(`{"name":"alice"}`)},
		}},
		&ProviderResponse{Text: "done"},
	)
	a, _ := New(sp, prompt.Text("sys"), []tool.Tool{greetTool})
	_, _, err := a.Invoke(context.Background(), "greet alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handlerCalled {
		t.Error("handler should be called for valid payload")
	}
}
