package eval

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestKeywordGrounding_AllPresent(t *testing.T) {
	kg, err := NewKeywordGrounding([]string{"go", "language"})
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	result, err := kg.Evaluate(context.Background(), EvalCase{
		ActualOutput: "Go is a programming language created at Google.",
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
	if result.EvaluatorName != "keyword_grounding" {
		t.Errorf("expected evaluator name %q, got %q", "keyword_grounding", result.EvaluatorName)
	}
}

func TestKeywordGrounding_NonePresent(t *testing.T) {
	kg, err := NewKeywordGrounding([]string{"python", "rust"})
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	result, err := kg.Evaluate(context.Background(), EvalCase{
		ActualOutput: "Go is a programming language.",
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 0.0 {
		t.Errorf("expected score 0.0, got %f", result.Score)
	}
	if !strings.Contains(result.Explanation, "python") {
		t.Errorf("expected explanation to mention missing keyword %q, got %q", "python", result.Explanation)
	}
	if !strings.Contains(result.Explanation, "rust") {
		t.Errorf("expected explanation to mention missing keyword %q, got %q", "rust", result.Explanation)
	}
}

func TestKeywordGrounding_PartialMatch(t *testing.T) {
	kg, err := NewKeywordGrounding([]string{"go", "python", "rust", "java"})
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	result, err := kg.Evaluate(context.Background(), EvalCase{
		ActualOutput: "Go and Java are popular languages.",
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	expected := 2.0 / 4.0 // "go" and "java" found out of 4
	if math.Abs(result.Score-expected) > 1e-9 {
		t.Errorf("expected score %f, got %f", expected, result.Score)
	}
	if !strings.Contains(result.Explanation, "python") {
		t.Errorf("expected explanation to mention missing keyword %q, got %q", "python", result.Explanation)
	}
	if !strings.Contains(result.Explanation, "rust") {
		t.Errorf("expected explanation to mention missing keyword %q, got %q", "rust", result.Explanation)
	}
}

func TestKeywordGrounding_CaseInsensitive(t *testing.T) {
	kg, err := NewKeywordGrounding([]string{"Go", "LANGUAGE", "Google"})
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	result, err := kg.Evaluate(context.Background(), EvalCase{
		ActualOutput: "go is a programming LANGUAGE created at google.",
	})
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}

	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 for case-insensitive match, got %f", result.Score)
	}
	if result.Explanation != "" {
		t.Errorf("expected empty explanation, got %q", result.Explanation)
	}
}

func TestKeywordGrounding_EmptyKeywords_ReturnsError(t *testing.T) {
	_, err := NewKeywordGrounding([]string{})
	if err == nil {
		t.Fatal("expected error for empty keywords, got nil")
	}
	if !strings.Contains(err.Error(), "keywords must not be empty") {
		t.Errorf("expected error message about empty keywords, got %q", err.Error())
	}
}
