package openai

import (
	"context"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tokencount/tiktoken"
)

// Estimator is a TokenEstimator for OpenAI models. Because OpenAI does not
// expose a server-side token counting API, it delegates entirely to the
// tiktoken BPE tokenizer.
type Estimator struct {
	tik *tiktoken.Estimator
}

// compile-time check: *Estimator satisfies agent.TokenEstimator.
var _ agent.TokenEstimator = (*Estimator)(nil)

// NewEstimator creates an OpenAI token estimator that delegates to the given
// tiktoken Estimator. The caller is responsible for constructing the tiktoken
// Estimator with an appropriate encoding (e.g. "o200k_base" for GPT-4o).
func NewEstimator(tik *tiktoken.Estimator) *Estimator {
	return &Estimator{tik: tik}
}

// EstimateTokens delegates token counting to the underlying tiktoken Estimator.
func (e *Estimator) EstimateTokens(ctx context.Context, params agent.ConverseParams) (int, error) {
	return e.tik.EstimateTokens(ctx, params)
}
