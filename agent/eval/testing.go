package eval

import (
	"context"
	"fmt"
	"testing"
)

// RunT runs an EvalSuite and reports results through Go's testing framework.
// Each eval case becomes a subtest, and each failed evaluator within a case
// calls t.Errorf with the evaluator name, score, and explanation.
//
// Usage in a _test.go file:
//
//	func TestRAGQuality(t *testing.T) {
//	    cases := []eval.EvalCase{...}
//	    evaluators := []eval.Evaluator{...}
//	    eval.RunT(t, cases, evaluators)
//	}
//
// Options:
//   - Pass eval.WithSuiteConcurrency(n) to run evaluations in parallel.
//   - The test context is derived from t via context.Background() with t.Deadline().
func RunT(t *testing.T, cases []EvalCase, evaluators []Evaluator, opts ...SuiteOption) {
	t.Helper()

	suite, err := NewEvalSuite(cases, evaluators, opts...)
	if err != nil {
		t.Fatalf("eval.RunT: failed to create suite: %v", err)
	}

	ctx := testContext(t)

	report, err := suite.Run(ctx)
	if err != nil {
		t.Fatalf("eval.RunT: suite run failed: %v", err)
	}

	// Report per-case results as subtests.
	for i, cr := range report.Results {
		caseName := fmt.Sprintf("case_%d", i)
		if cr.Case.Query != "" {
			caseName = truncateTestName(cr.Case.Query, 60)
		}

		t.Run(caseName, func(t *testing.T) {
			t.Helper()

			if cr.Error != "" {
				t.Errorf("evaluator error: %s", cr.Error)
			}

			for _, r := range cr.Results {
				if !r.Pass {
					msg := fmt.Sprintf("%s: score=%.2f (threshold not met)", r.EvaluatorName, r.Score)
					if r.Explanation != "" {
						msg += fmt.Sprintf(" — %s", r.Explanation)
					}
					t.Errorf("%s", msg)
				} else {
					t.Logf("%s: score=%.2f ✓", r.EvaluatorName, r.Score)
				}
			}
		})
	}

	// Log summary.
	t.Logf("eval summary: %d cases, %d evaluators", report.TotalCases, len(evaluators))
	for _, s := range report.Summaries {
		t.Logf("  %-20s mean=%.2f passed=%d failed=%d", s.EvaluatorName, s.MeanScore, s.Passed, s.Failed)
	}
}

// RunTSingle runs a single evaluator against a single case and fails the test
// if the result doesn't pass. Useful for focused, single-assertion eval checks.
//
// Usage:
//
//	func TestAnswerContainsKeywords(t *testing.T) {
//	    kg, _ := eval.NewKeywordGrounding([]string{"Go", "concurrency"})
//	    eval.RunTSingle(t, kg, eval.EvalCase{
//	        ActualOutput: agentResponse,
//	    })
//	}
func RunTSingle(t *testing.T, evaluator Evaluator, ec EvalCase) {
	t.Helper()

	ctx := testContext(t)

	result, err := evaluator.Evaluate(ctx, ec)
	if err != nil {
		t.Fatalf("eval.RunTSingle: %s returned error: %v", evaluator.Name(), err)
	}

	if !result.Pass {
		msg := fmt.Sprintf("%s: score=%.2f (threshold not met)", result.EvaluatorName, result.Score)
		if result.Explanation != "" {
			msg += fmt.Sprintf(" — %s", result.Explanation)
		}
		t.Errorf("%s", msg)
	} else {
		t.Logf("%s: score=%.2f ✓", result.EvaluatorName, result.Score)
	}
}

// AssertScore runs a single evaluator and asserts the score meets a minimum.
// Unlike RunTSingle which uses the evaluator's configured threshold, this
// lets you specify an explicit minimum score for the assertion.
//
// Usage:
//
//	eval.AssertScore(t, relevanceEval, myCase, 0.8)
func AssertScore(t *testing.T, evaluator Evaluator, ec EvalCase, minScore float64) {
	t.Helper()

	ctx := testContext(t)

	result, err := evaluator.Evaluate(ctx, ec)
	if err != nil {
		t.Fatalf("eval.AssertScore: %s returned error: %v", evaluator.Name(), err)
	}

	if result.Score < minScore {
		msg := fmt.Sprintf("%s: score=%.2f < minimum %.2f", result.EvaluatorName, result.Score, minScore)
		if result.Explanation != "" {
			msg += fmt.Sprintf(" — %s", result.Explanation)
		}
		t.Errorf("%s", msg)
	} else {
		t.Logf("%s: score=%.2f >= %.2f ✓", result.EvaluatorName, result.Score, minScore)
	}
}

// testContext returns a context with the test's deadline if one is set.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		t.Cleanup(cancel)
	}
	return ctx
}

// truncateTestName shortens a string for use as a subtest name, replacing
// problematic characters and truncating to maxLen.
func truncateTestName(s string, maxLen int) string {
	// Replace characters that are problematic in test names.
	cleaned := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '-', c == '.':
			cleaned = append(cleaned, c)
		case c == ' ':
			cleaned = append(cleaned, '_')
		default:
			// skip
		}
	}
	if len(cleaned) > maxLen {
		cleaned = cleaned[:maxLen]
	}
	return string(cleaned)
}
