package eval

import (
	"context"
	"strings"
	"testing"
)

func TestJSONStructure_ValidJSONWithAllKeys(t *testing.T) {
	js := NewJSONStructure([]string{"name", "age"})

	result, err := js.Evaluate(context.Background(), EvalCase{
		ActualOutput: `{"name": "Alice", "age": 30}`,
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
	if result.Explanation != "" {
		t.Errorf("expected empty explanation, got %q", result.Explanation)
	}
	if result.EvaluatorName != "json_structure" {
		t.Errorf("expected evaluator name %q, got %q", "json_structure", result.EvaluatorName)
	}
}

func TestJSONStructure_InvalidJSON(t *testing.T) {
	js := NewJSONStructure([]string{"name"})

	result, err := js.Evaluate(context.Background(), EvalCase{
		ActualOutput: `not valid json at all`,
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 0.0 {
		t.Errorf("expected score 0.0, got %f", result.Score)
	}
	if !strings.Contains(result.Explanation, "invalid JSON") {
		t.Errorf("expected explanation to contain %q, got %q", "invalid JSON", result.Explanation)
	}
	if result.EvaluatorName != "json_structure" {
		t.Errorf("expected evaluator name %q, got %q", "json_structure", result.EvaluatorName)
	}
}

func TestJSONStructure_ValidJSONMissingRequiredKey(t *testing.T) {
	js := NewJSONStructure([]string{"name", "email"})

	result, err := js.Evaluate(context.Background(), EvalCase{
		ActualOutput: `{"name": "Alice", "age": 30}`,
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 0.0 {
		t.Errorf("expected score 0.0, got %f", result.Score)
	}
	if !strings.Contains(result.Explanation, "email") {
		t.Errorf("expected explanation to mention missing key %q, got %q", "email", result.Explanation)
	}
	if result.EvaluatorName != "json_structure" {
		t.Errorf("expected evaluator name %q, got %q", "json_structure", result.EvaluatorName)
	}
}

func TestJSONStructure_NoRequiredKeysValidJSON(t *testing.T) {
	js := NewJSONStructure([]string{})

	result, err := js.Evaluate(context.Background(), EvalCase{
		ActualOutput: `{"anything": "goes", "count": 42}`,
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
	if result.Explanation != "" {
		t.Errorf("expected empty explanation, got %q", result.Explanation)
	}
	if result.EvaluatorName != "json_structure" {
		t.Errorf("expected evaluator name %q, got %q", "json_structure", result.EvaluatorName)
	}
}
