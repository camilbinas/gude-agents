package agent

import (
	"context"
	"errors"
	"testing"
)

// mockEstimator is a test TokenEstimator that returns configurable results.
type mockEstimator struct {
	estimate int
	err      error
}

func (m *mockEstimator) EstimateTokens(_ context.Context, _ ConverseParams) (int, error) {
	return m.estimate, m.err
}

func TestPreFlightCheck_NoEstimatorConfigured_ReturnsNil(t *testing.T) {
	// A RateLimiter without a TokenEstimator should skip the pre-flight check entirely.
	rl, err := NewRateLimiter(TPM(1000))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	ctx := context.Background()
	params := ConverseParams{System: "hello world"}

	if err := rl.PreFlightCheck(ctx, "key", params); err != nil {
		t.Fatalf("expected nil when no estimator configured, got: %v", err)
	}
}

func TestPreFlightCheck_EstimatorError_ReturnsNil_FailOpen(t *testing.T) {
	// When the estimator returns an error, PreFlightCheck should fail-open (return nil).
	estimator := &mockEstimator{estimate: 0, err: errors.New("estimation failed")}

	rl, err := NewRateLimiter(TPM(1000), WithTokenEstimator(estimator))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	ctx := context.Background()
	params := ConverseParams{System: "hello world"}

	if err := rl.PreFlightCheck(ctx, "key", params); err != nil {
		t.Fatalf("expected nil on estimator error (fail-open), got: %v", err)
	}
}

func TestPreFlightCheck_WithTokenEstimatorNil_DefaultsToCharEstimator(t *testing.T) {
	// WithTokenEstimator(nil) should use CharEstimator as the default.
	rl, err := NewRateLimiter(TPM(1000), WithTokenEstimator(nil))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	// Verify the tokenEstimator is set (not nil) — it should be CharEstimator.
	if rl.tokenEstimator == nil {
		t.Fatal("expected tokenEstimator to be set when WithTokenEstimator(nil) is used")
	}

	// CharEstimator on "hello" (5 chars) → ceil(5/4) = 2 tokens.
	// With TPM(1000), this should easily fit — PreFlightCheck returns nil.
	ctx := context.Background()
	params := ConverseParams{System: "hello"}

	if err := rl.PreFlightCheck(ctx, "key", params); err != nil {
		t.Fatalf("expected nil for small input within budget, got: %v", err)
	}
}

func TestPreFlightCheck_EstimateExceedsCapacity_ReturnsErrRateLimitExceeded(t *testing.T) {
	// When estimated tokens exceed remaining TPM capacity, return ErrRateLimitExceeded.
	estimator := &mockEstimator{estimate: 500, err: nil}

	rl, err := NewRateLimiter(TPM(100), WithTokenEstimator(estimator))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	ctx := context.Background()
	params := ConverseParams{System: "test"}

	err = rl.PreFlightCheck(ctx, "key", params)
	if !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("expected ErrRateLimitExceeded when estimate exceeds capacity, got: %v", err)
	}
}

func TestPreFlightCheck_EstimateWithinCapacity_ReturnsNil(t *testing.T) {
	// When estimated tokens fit within remaining TPM capacity, return nil.
	estimator := &mockEstimator{estimate: 50, err: nil}

	rl, err := NewRateLimiter(TPM(1000), WithTokenEstimator(estimator))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	ctx := context.Background()
	params := ConverseParams{System: "test"}

	if err := rl.PreFlightCheck(ctx, "key", params); err != nil {
		t.Fatalf("expected nil when estimate within capacity, got: %v", err)
	}
}
