package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// KeywordGrounding is a rule-based evaluator that checks whether required
// keywords appear in the actual output. The score equals the fraction of
// required keywords found (case-insensitive).
type KeywordGrounding struct {
	keywords []string
	cfg      evaluatorConfig
}

// NewKeywordGrounding creates a KeywordGrounding evaluator that checks for the
// presence of the given keywords. It returns an error if keywords is empty.
func NewKeywordGrounding(keywords []string, opts ...EvaluatorOption) (*KeywordGrounding, error) {
	if len(keywords) == 0 {
		return nil, errors.New("keywords must not be empty")
	}
	cfg := applyOptions(opts)
	return &KeywordGrounding{
		keywords: keywords,
		cfg:      cfg,
	}, nil
}

// Evaluate computes the fraction of required keywords found in the actual
// output (case-insensitive). Missing keywords are listed in the Explanation
// when the score is less than 1.0.
func (k *KeywordGrounding) Evaluate(_ context.Context, ec EvalCase) (EvalResult, error) {
	lower := strings.ToLower(ec.ActualOutput)

	var missing []string
	found := 0
	for _, kw := range k.keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			found++
		} else {
			missing = append(missing, kw)
		}
	}

	score := float64(found) / float64(len(k.keywords))

	// Clamp score to [0.0, 1.0].
	if score < 0.0 {
		score = 0.0
	}
	if score > 1.0 {
		score = 1.0
	}

	var explanation string
	if score < 1.0 {
		explanation = fmt.Sprintf("missing keywords: %s", strings.Join(missing, ", "))
	}

	return EvalResult{
		EvaluatorName: k.Name(),
		Score:         score,
		Pass:          applyThreshold(score, k.cfg),
		Explanation:   explanation,
	}, nil
}

// Name returns the evaluator name.
func (k *KeywordGrounding) Name() string {
	return "keyword_grounding"
}
