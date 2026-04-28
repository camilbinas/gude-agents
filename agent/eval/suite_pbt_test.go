package eval

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

// deterministicEvaluator returns a score derived from a hash of the case's Query
// combined with the evaluator's name, producing varied but deterministic scores.
type deterministicEvaluator struct {
	evalName  string
	threshold float64
}

func (d *deterministicEvaluator) Evaluate(_ context.Context, ec EvalCase) (EvalResult, error) {
	h := fnv.New64a()
	h.Write([]byte(ec.Query))
	h.Write([]byte(d.evalName))
	// Map hash to [0.0, 1.0].
	score := float64(h.Sum64()%10001) / 10000.0
	return EvalResult{
		EvaluatorName: d.evalName,
		Score:         score,
		Pass:          score >= d.threshold,
		Explanation:   "deterministic score",
	}, nil
}

func (d *deterministicEvaluator) Name() string {
	return d.evalName
}

// Feature: llm-evaluation, Property 7: Suite produces complete and correct report
// Validates: Requirements 10.2, 10.3, 10.4, 11.1, 11.2, 11.3
func TestProperty_SuiteCompleteness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "numCases")
		m := rapid.IntRange(1, 5).Draw(t, "numEvaluators")

		// Generate N random cases with unique queries.
		cases := make([]EvalCase, n)
		for i := range cases {
			cases[i] = EvalCase{
				Query:        fmt.Sprintf("query_%d_%s", i, rapid.StringMatching(`[a-z]{3,10}`).Draw(t, fmt.Sprintf("q_%d", i))),
				ActualOutput: rapid.StringMatching(`[a-zA-Z0-9 ]{1,40}`).Draw(t, fmt.Sprintf("out_%d", i)),
			}
		}

		// Generate M deterministic evaluators with a fixed threshold.
		threshold := 0.5
		evaluators := make([]Evaluator, m)
		for j := range evaluators {
			evaluators[j] = &deterministicEvaluator{
				evalName:  fmt.Sprintf("eval_%d", j),
				threshold: threshold,
			}
		}

		suite, err := NewEvalSuite(cases, evaluators)
		if err != nil {
			t.Fatalf("NewEvalSuite failed: %v", err)
		}

		report, err := suite.Run(context.Background())
		if err != nil {
			t.Fatalf("suite.Run failed: %v", err)
		}

		// Property: TotalCases == N
		if report.TotalCases != n {
			t.Fatalf("TotalCases: got %d, want %d", report.TotalCases, n)
		}

		// Property: total EvalResults == N * M
		totalResults := 0
		for _, cr := range report.Results {
			totalResults += len(cr.Results)
		}
		if totalResults != n*m {
			t.Fatalf("total EvalResults: got %d, want %d", totalResults, n*m)
		}

		// Property: per-evaluator Passed+Failed == N and MeanScore == arithmetic mean
		for j := 0; j < m; j++ {
			evalName := fmt.Sprintf("eval_%d", j)
			summary, ok := report.Summaries[evalName]
			if !ok {
				t.Fatalf("missing summary for evaluator %q", evalName)
			}

			if summary.Passed+summary.Failed != n {
				t.Fatalf("evaluator %q: Passed(%d)+Failed(%d) = %d, want %d",
					evalName, summary.Passed, summary.Failed,
					summary.Passed+summary.Failed, n)
			}

			// Compute expected mean score by running the evaluator on each case.
			var totalScore float64
			for i := 0; i < n; i++ {
				res, err := evaluators[j].Evaluate(context.Background(), cases[i])
				if err != nil {
					t.Fatalf("unexpected evaluator error: %v", err)
				}
				totalScore += res.Score
			}
			expectedMean := totalScore / float64(n)

			if math.Abs(summary.MeanScore-expectedMean) > 1e-9 {
				t.Fatalf("evaluator %q: MeanScore got %v, want %v",
					evalName, summary.MeanScore, expectedMean)
			}
		}
	})
}

// Feature: llm-evaluation, Property 9: Concurrent execution produces deterministic results
// Validates: Requirements 16.4
func TestProperty_ConcurrentDeterminism(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 15).Draw(t, "numCases")
		m := rapid.IntRange(1, 4).Draw(t, "numEvaluators")
		concurrency := rapid.IntRange(2, 8).Draw(t, "concurrency")

		// Generate random cases.
		cases := make([]EvalCase, n)
		for i := range cases {
			cases[i] = EvalCase{
				Query:        fmt.Sprintf("query_%d_%s", i, rapid.StringMatching(`[a-z]{3,10}`).Draw(t, fmt.Sprintf("q_%d", i))),
				ActualOutput: rapid.StringMatching(`[a-zA-Z0-9 ]{1,40}`).Draw(t, fmt.Sprintf("out_%d", i)),
			}
		}

		// Generate deterministic evaluators.
		evaluators := make([]Evaluator, m)
		for j := range evaluators {
			evaluators[j] = &deterministicEvaluator{
				evalName:  fmt.Sprintf("eval_%d", j),
				threshold: 0.5,
			}
		}

		// Run with concurrency=1 (sequential).
		suiteSeq, err := NewEvalSuite(cases, evaluators)
		if err != nil {
			t.Fatalf("NewEvalSuite (sequential) failed: %v", err)
		}
		reportSeq, err := suiteSeq.Run(context.Background())
		if err != nil {
			t.Fatalf("suite.Run (sequential) failed: %v", err)
		}

		// Run with concurrency=N (parallel).
		suitePar, err := NewEvalSuite(cases, evaluators, WithSuiteConcurrency(concurrency))
		if err != nil {
			t.Fatalf("NewEvalSuite (parallel) failed: %v", err)
		}
		reportPar, err := suitePar.Run(context.Background())
		if err != nil {
			t.Fatalf("suite.Run (parallel) failed: %v", err)
		}

		// Compare TotalCases.
		if reportSeq.TotalCases != reportPar.TotalCases {
			t.Fatalf("TotalCases mismatch: seq=%d, par=%d",
				reportSeq.TotalCases, reportPar.TotalCases)
		}

		// Compare Results (ignoring Timestamp).
		if len(reportSeq.Results) != len(reportPar.Results) {
			t.Fatalf("Results length mismatch: seq=%d, par=%d",
				len(reportSeq.Results), len(reportPar.Results))
		}

		for i := range reportSeq.Results {
			seqCR := reportSeq.Results[i]
			parCR := reportPar.Results[i]

			if seqCR.Case.Query != parCR.Case.Query {
				t.Fatalf("case %d: Query mismatch: seq=%q, par=%q",
					i, seqCR.Case.Query, parCR.Case.Query)
			}
			if seqCR.Error != parCR.Error {
				t.Fatalf("case %d: Error mismatch: seq=%q, par=%q",
					i, seqCR.Error, parCR.Error)
			}

			// Sort results by evaluator name for stable comparison.
			seqResults := sortResultsByName(seqCR.Results)
			parResults := sortResultsByName(parCR.Results)

			if len(seqResults) != len(parResults) {
				t.Fatalf("case %d: Results count mismatch: seq=%d, par=%d",
					i, len(seqResults), len(parResults))
			}

			for j := range seqResults {
				if seqResults[j].EvaluatorName != parResults[j].EvaluatorName {
					t.Fatalf("case %d result %d: EvaluatorName mismatch: seq=%q, par=%q",
						i, j, seqResults[j].EvaluatorName, parResults[j].EvaluatorName)
				}
				if seqResults[j].Score != parResults[j].Score {
					t.Fatalf("case %d result %d: Score mismatch: seq=%v, par=%v",
						i, j, seqResults[j].Score, parResults[j].Score)
				}
				if seqResults[j].Pass != parResults[j].Pass {
					t.Fatalf("case %d result %d: Pass mismatch: seq=%v, par=%v",
						i, j, seqResults[j].Pass, parResults[j].Pass)
				}
			}
		}

		// Compare Summaries.
		if len(reportSeq.Summaries) != len(reportPar.Summaries) {
			t.Fatalf("Summaries count mismatch: seq=%d, par=%d",
				len(reportSeq.Summaries), len(reportPar.Summaries))
		}

		for name, seqSummary := range reportSeq.Summaries {
			parSummary, ok := reportPar.Summaries[name]
			if !ok {
				t.Fatalf("missing summary for %q in parallel report", name)
			}
			if seqSummary.Passed != parSummary.Passed {
				t.Fatalf("summary %q: Passed mismatch: seq=%d, par=%d",
					name, seqSummary.Passed, parSummary.Passed)
			}
			if seqSummary.Failed != parSummary.Failed {
				t.Fatalf("summary %q: Failed mismatch: seq=%d, par=%d",
					name, seqSummary.Failed, parSummary.Failed)
			}
			if math.Abs(seqSummary.MeanScore-parSummary.MeanScore) > 1e-9 {
				t.Fatalf("summary %q: MeanScore mismatch: seq=%v, par=%v",
					name, seqSummary.MeanScore, parSummary.MeanScore)
			}
		}
	})
}

// sortResultsByName returns a copy of results sorted by EvaluatorName for stable comparison.
func sortResultsByName(results []EvalResult) []EvalResult {
	sorted := make([]EvalResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].EvaluatorName < sorted[j].EvaluatorName
	})
	return sorted
}
