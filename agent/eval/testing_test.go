package eval

import (
	"context"
	"testing"
)

// mockPassEvaluator always returns a passing score.
type mockPassEvaluator struct{}

func (m *mockPassEvaluator) Evaluate(_ context.Context, _ EvalCase) (EvalResult, error) {
	return EvalResult{
		EvaluatorName: "mock_pass",
		Score:         0.9,
		Pass:          true,
		Explanation:   "all good",
	}, nil
}
func (m *mockPassEvaluator) Name() string { return "mock_pass" }

// mockFailEvaluator always returns a failing score.
type mockFailEvaluator struct{}

func (m *mockFailEvaluator) Evaluate(_ context.Context, _ EvalCase) (EvalResult, error) {
	return EvalResult{
		EvaluatorName: "mock_fail",
		Score:         0.2,
		Pass:          false,
		Explanation:   "quality too low",
	}, nil
}
func (m *mockFailEvaluator) Name() string { return "mock_fail" }

func TestRunT_AllPass(t *testing.T) {
	cases := []EvalCase{
		{Query: "What is Go?", ActualOutput: "Go is a programming language."},
		{Query: "What is Rust?", ActualOutput: "Rust is a systems language."},
	}

	// RunT should not fail the test when all evaluators pass.
	RunT(t, cases, []Evaluator{&mockPassEvaluator{}})
}

func TestRunT_FailureReported(t *testing.T) {
	cases := []EvalCase{
		{Query: "test", ActualOutput: "bad output"},
	}

	// Verify that a failing evaluator produces the expected result.
	// We can't test t.Errorf was called without a mock testing.T,
	// so we verify the evaluator correctly identifies the failure.
	kg, err := NewKeywordGrounding([]string{"nonexistent_keyword_xyz"}, WithThreshold(1.0))
	if err != nil {
		t.Fatal(err)
	}

	result, evalErr := kg.Evaluate(context.Background(), cases[0])
	if evalErr != nil {
		t.Fatalf("unexpected error: %v", evalErr)
	}
	if result.Pass {
		t.Error("expected keyword evaluator to fail on missing keyword")
	}
	if result.Score != 0.0 {
		t.Errorf("expected score 0.0, got %f", result.Score)
	}
}

func TestRunTSingle_Pass(t *testing.T) {
	kg, err := NewKeywordGrounding([]string{"Go", "language"}, WithThreshold(0.5))
	if err != nil {
		t.Fatal(err)
	}

	RunTSingle(t, kg, EvalCase{
		ActualOutput: "Go is a programming language.",
	})
}

func TestAssertScore_Pass(t *testing.T) {
	kg, err := NewKeywordGrounding([]string{"Go", "language"})
	if err != nil {
		t.Fatal(err)
	}

	AssertScore(t, kg, EvalCase{
		ActualOutput: "Go is a programming language.",
	}, 0.9)
}

func TestAssertScore_ExactBoundary(t *testing.T) {
	kg, err := NewKeywordGrounding([]string{"Go", "language"})
	if err != nil {
		t.Fatal(err)
	}

	// Score is 1.0, minScore is 1.0 — should pass.
	AssertScore(t, kg, EvalCase{
		ActualOutput: "Go is a programming language.",
	}, 1.0)
}

func TestTruncateTestName(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"simple", 60, "simple"},
		{"What is Go?", 60, "What_is_Go"},
		{"Hello World! How are you?", 10, "Hello_Worl"},
		{"special/chars\\here", 60, "specialcharshere"},
		{"", 60, ""},
	}

	for _, tc := range tests {
		got := truncateTestName(tc.input, tc.maxLen)
		if got != tc.expected {
			t.Errorf("truncateTestName(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.expected)
		}
	}
}
