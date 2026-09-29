package openai

import (
	"context"

	"github.com/camilbinas/gude-agents/agent"
)

// Estimator is a TokenEstimator for OpenAI models. Because OpenAI does not
// expose a server-side token counting API, it delegates to another estimator,
// typically the tiktoken BPE tokenizer.
type Estimator struct {
	estimator agent.TokenEstimator
}

// compile-time check: *Estimator satisfies agent.TokenEstimator.
var _ agent.TokenEstimator = (*Estimator)(nil)

// NewEstimator creates an OpenAI token estimator that delegates to estimator.
func NewEstimator(estimator agent.TokenEstimator) *Estimator {
	return &Estimator{estimator: estimator}
}

// EstimateTokens delegates token counting to the underlying estimator.
func (e *Estimator) EstimateTokens(ctx context.Context, req agent.ModelRequest) (int, error) {
	return e.estimator.EstimateTokens(ctx, req)
}
