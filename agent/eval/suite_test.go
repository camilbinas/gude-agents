package eval

import (
	"context"
	"errors"
	"testing"
)

// mockEvaluator implements Evaluator with a fixed score and optional error.
type mockEvaluator struct {
	name  string
	score float64
	err   error
}

func (m *mockEvaluator) Evaluate(_ context.Context, ec EvalCase) (EvalResult, error) {
	if m.err != nil {
		return EvalResult{}, m.err
	}
	return EvalResult{
		EvaluatorName: m.name,
		Score:         m.score,
		Pass:          m.score >= 0.5,
		Explanation:   "mock explanation",
	}, nil
}

func (m *mockEvaluator) Name() string {
	return m.name
}

func TestSuite_SingleCaseSingleEvaluator(t *testing.T) {
	cases := []EvalCase{
		{Query: "What is Go?", ActualOutput: "Go is a programming language."},
	}
	evaluators := []Evaluator{
		&mockEvaluator{name: "mock_eval", score: 0.8},
	}

	suite, err := NewEvalSuite(cases, evaluators)
	if err != nil {
		t.Fatalf("unexpected error creating suite: %v", err)
	}

	report, err := suite.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error running suite: %v", err)
	}

	if report.TotalCases != 1 {
		t.Errorf("expected TotalCases=1, got %d", report.TotalCases)
	}
	if len(report.Results) != 1 {
		t.Fatalf("expected 1 CaseResults, got %d", len(report.Results))
	}

	cr := report.Results[0]
	if len(cr.Results) != 1 {
		t.Fatalf("expected 1 EvalResult, got %d", len(cr.Results))
	}
	if cr.Results[0].EvaluatorName != "mock_eval" {
		t.Errorf("expected evaluator name %q, got %q", "mock_eval", cr.Results[0].EvaluatorName)
	}
	if cr.Results[0].Score != 0.8 {
		t.Errorf("expected score 0.8, got %f", cr.Results[0].Score)
	}
	if cr.Error != "" {
		t.Errorf("expected no error, got %q", cr.Error)
	}

	// Verify summary
	summary, ok := report.Summaries["mock_eval"]
	if !ok {
		t.Fatal("expected summary for mock_eval")
	}
	if summary.MeanScore != 0.8 {
		t.Errorf("expected MeanScore=0.8, got %f", summary.MeanScore)
	}
	if summary.Passed != 1 {
		t.Errorf("expected Passed=1, got %d", summary.Passed)
	}
	if summary.Failed != 0 {
		t.Errorf("expected Failed=0, got %d", summary.Failed)
	}
}

func TestSuite_EvaluatorError_RecordedAndOtherCasesEvaluated(t *testing.T) {
	cases := []EvalCase{
		{Query: "case1", ActualOutput: "output1"},
		{Query: "case2", ActualOutput: "output2"},
	}
	evaluators := []Evaluator{
		&mockEvaluator{name: "failing_eval", err: errors.New("provider timeout")},
	}

	suite, err := NewEvalSuite(cases, evaluators)
	if err != nil {
		t.Fatalf("unexpected error creating suite: %v", err)
	}

	report, err := suite.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error running suite: %v", err)
	}

	if report.TotalCases != 2 {
		t.Errorf("expected TotalCases=2, got %d", report.TotalCases)
	}

	// Both cases should have errors recorded
	for i, cr := range report.Results {
		if cr.Error == "" {
			t.Errorf("case %d: expected error to be recorded, got empty", i)
		}
		if len(cr.Results) != 0 {
			t.Errorf("case %d: expected 0 results (error case), got %d", i, len(cr.Results))
		}
	}

	// Summary should reflect failures
	summary, ok := report.Summaries["failing_eval"]
	if !ok {
		t.Fatal("expected summary for failing_eval")
	}
	if summary.Failed != 2 {
		t.Errorf("expected Failed=2, got %d", summary.Failed)
	}
}

func TestSuite_CustomEvaluatorAlongsideBuiltIn(t *testing.T) {
	cases := []EvalCase{
		{Query: "test query", ActualOutput: `{"key": "value"}`},
	}

	jsonEval := NewJSONStructure([]string{"key"})

	customEval := &mockEvaluator{name: "custom_eval", score: 0.9}

	evaluators := []Evaluator{jsonEval, customEval}

	suite, err := NewEvalSuite(cases, evaluators)
	if err != nil {
		t.Fatalf("unexpected error creating suite: %v", err)
	}

	report, err := suite.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error running suite: %v", err)
	}

	if report.TotalCases != 1 {
		t.Errorf("expected TotalCases=1, got %d", report.TotalCases)
	}

	cr := report.Results[0]
	if len(cr.Results) != 2 {
		t.Fatalf("expected 2 EvalResults, got %d", len(cr.Results))
	}

	// Verify both evaluators ran
	names := map[string]bool{}
	for _, r := range cr.Results {
		names[r.EvaluatorName] = true
	}
	if !names["json_structure"] {
		t.Error("expected json_structure evaluator result")
	}
	if !names["custom_eval"] {
		t.Error("expected custom_eval evaluator result")
	}

	// Verify summaries exist for both
	if _, ok := report.Summaries["json_structure"]; !ok {
		t.Error("expected summary for json_structure")
	}
	if _, ok := report.Summaries["custom_eval"]; !ok {
		t.Error("expected summary for custom_eval")
	}
}

func TestSuite_DefaultConcurrencyIsSequential(t *testing.T) {
	cases := []EvalCase{
		{Query: "q1", ActualOutput: "o1"},
	}
	evaluators := []Evaluator{
		&mockEvaluator{name: "eval1", score: 0.7},
	}

	suite, err := NewEvalSuite(cases, evaluators)
	if err != nil {
		t.Fatalf("unexpected error creating suite: %v", err)
	}

	// The default concurrency should be 1 (sequential).
	if suite.concurrency != 1 {
		t.Errorf("expected default concurrency=1, got %d", suite.concurrency)
	}
}

func TestSuite_NilCasesOrNilEvaluators_ReturnsError(t *testing.T) {
	t.Run("nil cases", func(t *testing.T) {
		_, err := NewEvalSuite(nil, []Evaluator{&mockEvaluator{name: "e", score: 0.5}})
		if err == nil {
			t.Fatal("expected error for nil cases, got nil")
		}
	})

	t.Run("empty cases", func(t *testing.T) {
		_, err := NewEvalSuite([]EvalCase{}, []Evaluator{&mockEvaluator{name: "e", score: 0.5}})
		if err == nil {
			t.Fatal("expected error for empty cases, got nil")
		}
	})

	t.Run("nil evaluators", func(t *testing.T) {
		_, err := NewEvalSuite([]EvalCase{{Query: "q"}}, nil)
		if err == nil {
			t.Fatal("expected error for nil evaluators, got nil")
		}
	})

	t.Run("empty evaluators", func(t *testing.T) {
		_, err := NewEvalSuite([]EvalCase{{Query: "q"}}, []Evaluator{})
		if err == nil {
			t.Fatal("expected error for empty evaluators, got nil")
		}
	})
}
