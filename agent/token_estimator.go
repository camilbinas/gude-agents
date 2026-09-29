package agent

import "context"

// TokenEstimator computes an approximate input token count from a model
// request before the provider call is made. Implementations must be safe for
// concurrent use.
type TokenEstimator interface {
	EstimateTokens(ctx context.Context, req ModelRequest) (int, error)
}
