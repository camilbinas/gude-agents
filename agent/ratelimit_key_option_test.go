package agent

import (
	"context"
	"strings"
	"testing"
)

type noopLimiter struct{}

func (noopLimiter) Reserve(context.Context, RateLimitRequest) (RateLimitLease, error) {
	return nil, nil
}
func (noopLimiter) Commit(context.Context, RateLimitLease, TokenUsage) error { return nil }
func (noopLimiter) Release(context.Context, RateLimitLease) error            { return nil }

func TestWithRateLimitKeyRejectsNil(t *testing.T) {
	_, err := New(mockProvider{}, "sys", WithRateLimiter(noopLimiter{}), WithRateLimitKey(nil))
	if err == nil || !strings.Contains(err.Error(), "must not be nil") {
		t.Fatalf("New error = %v, want nil selector rejected", err)
	}
}

func TestWithRateLimitKeyRequiresRateLimiter(t *testing.T) {
	_, err := New(mockProvider{}, "sys", WithRateLimitKey(func(*Context) string { return "k" }))
	if err == nil || !strings.Contains(err.Error(), "requires WithRateLimiter") {
		t.Fatalf("New error = %v, want a selector without a limiter rejected", err)
	}
	// The order of the options does not matter.
	if _, err := New(mockProvider{}, "sys",
		WithRateLimitKey(func(*Context) string { return "k" }), WithRateLimiter(noopLimiter{})); err != nil {
		t.Fatalf("selector before limiter: %v", err)
	}
}

func TestWithToolVisibilityRejectsNilRule(t *testing.T) {
	if _, err := New(mockProvider{}, "sys", WithToolVisibility(nil)); err == nil {
		t.Fatal("expected a nil visibility rule to be rejected")
	}
}
