package eval

import (
	"context"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// EvalCase is a single evaluation test case.
type EvalCase struct {
	Query            string            `json:"query"`
	ActualOutput     string            `json:"actual_output"`
	RetrievedContext []agent.Document  `json:"retrieved_context"`
	ReferenceAnswer  string            `json:"reference_answer,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// EvalResult is the outcome of a single evaluator applied to a single EvalCase.
type EvalResult struct {
	EvaluatorName string  `json:"evaluator_name"`
	Score         float64 `json:"score"`
	Pass          bool    `json:"pass"`
	Explanation   string  `json:"explanation"`
}

// EvalReport is the aggregated output of running an EvalSuite.
type EvalReport struct {
	Timestamp  time.Time              `json:"timestamp"`
	TotalCases int                    `json:"total_cases"`
	Results    []CaseResults          `json:"results"`
	Summaries  map[string]EvalSummary `json:"summaries"`
}

// CaseResults groups all EvalResults for a single EvalCase.
type CaseResults struct {
	Case    EvalCase     `json:"case"`
	Results []EvalResult `json:"results"`
	Error   string       `json:"error,omitempty"`
}

// EvalSummary holds aggregated metrics for a single evaluator across all cases.
type EvalSummary struct {
	EvaluatorName string  `json:"evaluator_name"`
	MeanScore     float64 `json:"mean_score"`
	Passed        int     `json:"passed"`
	Failed        int     `json:"failed"`
}

// Evaluator scores an EvalCase and produces an EvalResult.
type Evaluator interface {
	Evaluate(ctx context.Context, ec EvalCase) (EvalResult, error)
	Name() string
}

// evaluatorConfig holds shared configuration for evaluators.
type evaluatorConfig struct {
	threshold float64
}

// EvaluatorOption configures an evaluator.
type EvaluatorOption func(*evaluatorConfig)

// WithThreshold sets the pass/fail threshold (default 0.5).
func WithThreshold(t float64) EvaluatorOption {
	return func(cfg *evaluatorConfig) {
		cfg.threshold = t
	}
}

// defaultEvaluatorConfig returns an evaluatorConfig with default values.
func defaultEvaluatorConfig() evaluatorConfig {
	return evaluatorConfig{
		threshold: 0.5,
	}
}

// applyOptions applies functional options to a default config.
func applyOptions(opts []EvaluatorOption) evaluatorConfig {
	cfg := defaultEvaluatorConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// applyThreshold returns true if the score meets or exceeds the configured threshold.
func applyThreshold(score float64, cfg evaluatorConfig) bool {
	return score >= cfg.threshold
}

// ResultsForEvaluator returns all EvalResults for the named evaluator across all cases.
func (r *EvalReport) ResultsForEvaluator(name string) []EvalResult {
	var out []EvalResult
	for _, cr := range r.Results {
		for _, er := range cr.Results {
			if er.EvaluatorName == name {
				out = append(out, er)
			}
		}
	}
	return out
}
