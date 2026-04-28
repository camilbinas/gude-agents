package eval

import (
	"context"
	"errors"
	"sync"
	"time"
)

// SuiteOption configures an EvalSuite.
type SuiteOption func(*EvalSuite)

// WithSuiteConcurrency sets the maximum number of parallel evaluations.
// Default is 1 (sequential). Higher values speed up evaluation but increase
// concurrent load on LLM providers.
func WithSuiteConcurrency(n int) SuiteOption {
	return func(s *EvalSuite) {
		if n < 1 {
			n = 1
		}
		s.concurrency = n
	}
}

// EvalSuite groups test cases and evaluators for batch execution.
type EvalSuite struct {
	cases       []EvalCase
	evaluators  []Evaluator
	concurrency int
}

// NewEvalSuite creates a new EvalSuite with the given cases and evaluators.
// Returns an error if cases or evaluators is nil or empty.
func NewEvalSuite(cases []EvalCase, evaluators []Evaluator, opts ...SuiteOption) (*EvalSuite, error) {
	if len(cases) == 0 {
		return nil, errors.New("eval suite: cases must not be nil or empty")
	}
	if len(evaluators) == 0 {
		return nil, errors.New("eval suite: evaluators must not be nil or empty")
	}

	s := &EvalSuite{
		cases:       cases,
		evaluators:  evaluators,
		concurrency: 1,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Run executes all evaluators against all cases and returns an aggregated
// EvalReport. When concurrency > 1, evaluations run in parallel using a
// bounded worker pool (semaphore channel pattern).
//
// Per-case errors are recorded in CaseResults.Error and do not abort the run.
// Context cancellation stops dispatching new work but waits for in-flight
// evaluations to complete.
func (s *EvalSuite) Run(ctx context.Context) (EvalReport, error) {
	numCases := len(s.cases)
	numEvals := len(s.evaluators)

	// Pre-allocate a flat results slice indexed by (caseIdx * numEvals + evalIdx)
	// so goroutines write to distinct slots without contention.
	results := make([]EvalResult, numCases*numEvals)
	errs := make([]error, numCases*numEvals)

	if s.concurrency <= 1 {
		// Sequential path — no goroutine overhead.
		for ci := 0; ci < numCases; ci++ {
			for ei := 0; ei < numEvals; ei++ {
				idx := ci*numEvals + ei
				res, err := s.evaluators[ei].Evaluate(ctx, s.cases[ci])
				results[idx] = res
				errs[idx] = err
			}
		}
	} else {
		// Parallel path — bounded concurrency via semaphore.
		sem := make(chan struct{}, s.concurrency)
		var wg sync.WaitGroup

		for ci := 0; ci < numCases; ci++ {
			for ei := 0; ei < numEvals; ei++ {
				// Check context before dispatching new work.
				select {
				case <-ctx.Done():
					// Stop dispatching but wait for in-flight work below.
					goto done
				default:
				}

				wg.Add(1)
				go func(caseIdx, evalIdx int) {
					defer wg.Done()

					sem <- struct{}{}        // acquire
					defer func() { <-sem }() // release

					idx := caseIdx*numEvals + evalIdx
					res, err := s.evaluators[evalIdx].Evaluate(ctx, s.cases[caseIdx])
					results[idx] = res
					errs[idx] = err
				}(ci, ei)
			}
		}
	done:
		wg.Wait()
	}

	// Aggregate results into CaseResults.
	caseResults := make([]CaseResults, numCases)
	for ci := 0; ci < numCases; ci++ {
		cr := CaseResults{
			Case:    s.cases[ci],
			Results: make([]EvalResult, 0, numEvals),
		}

		var caseErrs []string
		for ei := 0; ei < numEvals; ei++ {
			idx := ci*numEvals + ei
			if errs[idx] != nil {
				caseErrs = append(caseErrs, errs[idx].Error())
			} else {
				cr.Results = append(cr.Results, results[idx])
			}
		}

		if len(caseErrs) > 0 {
			cr.Error = joinErrors(caseErrs)
		}

		caseResults[ci] = cr
	}

	// Compute per-evaluator summaries.
	summaries := make(map[string]EvalSummary, numEvals)
	for _, ev := range s.evaluators {
		name := ev.Name()
		var totalScore float64
		var passed, failed int

		for ci := 0; ci < numCases; ci++ {
			idx := ci * numEvals
			// Find the result for this evaluator in this case's results.
			for ei := 0; ei < numEvals; ei++ {
				if s.evaluators[ei].Name() == name {
					slot := idx + ei
					if errs[slot] != nil {
						failed++
					} else {
						totalScore += results[slot].Score
						if results[slot].Pass {
							passed++
						} else {
							failed++
						}
					}
					break
				}
			}
		}

		summaries[name] = EvalSummary{
			EvaluatorName: name,
			MeanScore:     totalScore / float64(numCases),
			Passed:        passed,
			Failed:        failed,
		}
	}

	return EvalReport{
		Timestamp:  time.Now(),
		TotalCases: numCases,
		Results:    caseResults,
		Summaries:  summaries,
	}, nil
}

// joinErrors concatenates multiple error strings with "; " separator.
func joinErrors(errs []string) string {
	if len(errs) == 1 {
		return errs[0]
	}
	result := errs[0]
	for _, e := range errs[1:] {
		result += "; " + e
	}
	return result
}
