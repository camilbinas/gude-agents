package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

// countingProviderForPreflight tracks how many times ConverseStream is called.
type countingProviderForPreflight struct {
	calls    atomic.Int32
	response *ProviderResponse
}

func (p *countingProviderForPreflight) Name() string { return "counting" }

func (p *countingProviderForPreflight) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	p.calls.Add(1)
	return p.response, nil
}

func (p *countingProviderForPreflight) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	p.calls.Add(1)
	if p.response.Text != "" && cb != nil {
		cb(p.response.Text)
	}
	return p.response, nil
}

func TestLoopIntegration_PreFlightReject_NoProviderCall(t *testing.T) {
	// When the pre-flight check rejects (estimate exceeds capacity),
	// the provider should NOT be called at all.
	estimator := &mockEstimator{estimate: 5000, err: nil}

	rl, err := NewRateLimiter(TPM(100), WithTokenEstimator(estimator))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	provider := &countingProviderForPreflight{
		response: &ProviderResponse{Text: "should not reach here"},
	}

	a, err := New(provider, prompt.Text("sys"), nil, WithRateLimiter(rl))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, invokeErr := a.Invoke(Background(), "hello")
	if invokeErr == nil {
		t.Fatal("expected error from pre-flight rejection, got nil")
	}
	if !errors.Is(invokeErr, ErrRateLimitExceeded) {
		t.Fatalf("expected ErrRateLimitExceeded, got: %v", invokeErr)
	}

	if calls := provider.calls.Load(); calls != 0 {
		t.Errorf("expected 0 provider calls when pre-flight rejects, got %d", calls)
	}
}

func TestLoopIntegration_PreFlightPass_NormalProviderCall(t *testing.T) {
	// When the pre-flight check passes (estimate within capacity),
	// the provider should be called normally and return a result.
	estimator := &mockEstimator{estimate: 10, err: nil}

	rl, err := NewRateLimiter(TPM(1000), WithTokenEstimator(estimator))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	provider := &countingProviderForPreflight{
		response: &ProviderResponse{
			Text:  "hello back",
			Usage: TokenUsage{InputTokens: 5, OutputTokens: 5},
		},
	}

	a, err := New(provider, prompt.Text("sys"), nil, WithRateLimiter(rl))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, invokeErr := a.Invoke(Background(), "hello")
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr)
	}
	if result != "hello back" {
		t.Errorf("expected %q, got %q", "hello back", result)
	}

	if calls := provider.calls.Load(); calls != 1 {
		t.Errorf("expected 1 provider call when pre-flight passes, got %d", calls)
	}
}

func TestLoopIntegration_NoRateLimiter_SkipsPreFlight(t *testing.T) {
	// When no rate limiter is configured, the agent should skip pre-flight
	// entirely and proceed to call the provider normally.
	provider := &countingProviderForPreflight{
		response: &ProviderResponse{
			Text:  "no limiter response",
			Usage: TokenUsage{InputTokens: 10, OutputTokens: 10},
		},
	}

	// No WithRateLimiter option — rateLimiter is nil.
	a, err := New(provider, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, invokeErr := a.Invoke(Background(), "hello")
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr)
	}
	if result != "no limiter response" {
		t.Errorf("expected %q, got %q", "no limiter response", result)
	}

	if calls := provider.calls.Load(); calls != 1 {
		t.Errorf("expected 1 provider call without rate limiter, got %d", calls)
	}
}

func TestLoopIntegration_PreFlightReject_NotRetried(t *testing.T) {
	// ErrRateLimitExceeded from pre-flight should NOT be retried,
	// even when the agent has retry configured.
	estimator := &mockEstimator{estimate: 5000, err: nil}

	rl, err := NewRateLimiter(TPM(100), WithTokenEstimator(estimator))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	provider := &countingProviderForPreflight{
		response: &ProviderResponse{Text: "should not reach here"},
	}

	a, err := New(provider, prompt.Text("sys"), nil,
		WithRateLimiter(rl),
		WithRetry(3, 10*time.Millisecond), // 3 retries configured
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, invokeErr := a.Invoke(Background(), "hello")
	if invokeErr == nil {
		t.Fatal("expected error from pre-flight rejection, got nil")
	}
	if !errors.Is(invokeErr, ErrRateLimitExceeded) {
		t.Fatalf("expected ErrRateLimitExceeded, got: %v", invokeErr)
	}

	// The provider should never have been called — pre-flight runs before
	// the retry loop and short-circuits on rejection.
	if calls := provider.calls.Load(); calls != 0 {
		t.Errorf("expected 0 provider calls (pre-flight reject should not retry), got %d", calls)
	}
}
