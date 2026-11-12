package ratelimit

import (
	"context"
	"errors"
	"testing"

	agent "github.com/camilbinas/gude-agents/agent"
)

// erroringEstimator always fails, and records the ctx it was handed so the test
// can verify the caller ctx (not context.Background) is threaded through.
type erroringEstimator struct{ gotCtx context.Context }

func (e *erroringEstimator) EstimateTokens(ctx context.Context, _ ModelRequest) (int, error) {
	e.gotCtx = ctx
	return 123, errors.New("estimator unavailable")
}

func tokenAmount(r Reservation) int {
	if len(r.Tokens) == 0 {
		return 0
	}
	return r.Tokens[0].Amount
}

func TestReservation_EstimatorErrorKeepsFallbackFloor(t *testing.T) {
	est := &erroringEstimator{}
	r := limiterForTest(t,
		TPM(1000),
		WithTokenEstimator(est),
		WithOutputReservationFallback(50),
	)

	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "sentinel")
	res := r.reservation(ctx, "id", agent.RateLimitRequest{Key: "k"})

	// On estimator error the input estimate (123) is dropped, but the output
	// fallback floor (50) is kept rather than reserving 0.
	if got := tokenAmount(res); got != 50 {
		t.Errorf("reserved tokens = %d, want 50 (fallback floor, estimator dropped)", got)
	}
	// The caller ctx must reach the estimator, not a fresh background ctx.
	if est.gotCtx == nil || est.gotCtx.Value(ctxKey{}) != "sentinel" {
		t.Errorf("estimator did not receive the caller ctx: %v", est.gotCtx)
	}
}

func TestReservation_EstimatorSuccessAddsToFloor(t *testing.T) {
	r := limiterForTest(t,
		TPM(1000),
		WithTokenEstimator(fixedEstimator{n: 30}),
		WithOutputReservationFallback(50),
	)
	res := r.reservation(context.Background(), "id", agent.RateLimitRequest{Key: "k"})
	if got := tokenAmount(res); got != 80 { // 50 floor + 30 estimate
		t.Errorf("reserved tokens = %d, want 80 (floor + estimate)", got)
	}
}

func TestReservation_MaxTokensAddedOnTop(t *testing.T) {
	maxOut := 200
	r := limiterForTest(t,
		TPM(5000),
		WithTokenEstimator(fixedEstimator{n: 30}),
		WithOutputReservationFallback(50),
	)
	req := agent.RateLimitRequest{Key: "k"}
	req.Request.InferenceConfig = &agent.InferenceConfig{MaxTokens: &maxOut}
	res := r.reservation(context.Background(), "id", req)
	if got := tokenAmount(res); got != 280 { // 50 + 30 + 200
		t.Errorf("reserved tokens = %d, want 280 (floor + estimate + max_tokens)", got)
	}
}
