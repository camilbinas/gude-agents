package integration_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/fallback"
)

// Resilience integration tests: retry, timeout, and fallback provider.
//
// Run with:
//   go test -v -timeout=120s -run TestIntegration_Resilience ./...

// failNProvider wraps a real provider and fails the first N Stream calls.
type failNProvider struct {
	inner     agent.Provider
	failsLeft atomic.Int32
}

func newFailNProvider(inner agent.Provider, failCount int) *failNProvider {
	p := &failNProvider{inner: inner}
	p.failsLeft.Store(int32(failCount))
	return p
}

func (p *failNProvider) Name() string { return "mock" }

func (p *failNProvider) Stream(ctx context.Context, req agent.ModelRequest, emit func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	if p.failsLeft.Add(-1) >= 0 {
		return nil, errors.New("simulated transient error")
	}
	return p.inner.Stream(ctx, req, emit)
}

func TestIntegration_Resilience_RetryRecovers(t *testing.T) {
	t.Parallel()
	real := newTestProvider(t)
	flaky := newFailNProvider(real, 2)

	a, err := agent.New(
		flaky,
		"You are a helpful assistant. Be very brief.",
		agent.WithProviderRetry(3, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := a.Invoke(agent.NewContext(ctx), "What is 2+2? Reply with just the number.")
	if err != nil {
		t.Fatalf("expected retry to recover, got error: %v", err)
	}
	if !strings.Contains(result.Text, "4") {
		t.Errorf("expected response to contain '4', got: %s", result.Text)
	}
	t.Logf("Retry recovered, response: %s", result.Text)
}

func TestIntegration_Resilience_RetryExhausted(t *testing.T) {
	t.Parallel()
	real := newTestProvider(t)
	flaky := newFailNProvider(real, 10)

	a, err := agent.New(
		flaky,
		"You are a helpful assistant.",
		agent.WithProviderRetry(2, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = a.Invoke(agent.NewContext(ctx), "Hello")
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}
	t.Logf("Retries exhausted as expected: %v", err)
}

func TestIntegration_Resilience_TimeoutEnforced(t *testing.T) {
	t.Parallel()
	real := newTestProvider(t)

	a, err := agent.New(
		real,
		"You are a helpful assistant. Write a very long essay about the history of computing.",
		agent.WithProviderTimeout(time.Nanosecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = a.Invoke(agent.NewContext(ctx), "Write a 1000 word essay.")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	t.Logf("Timeout enforced: %v", err)
}

func TestIntegration_Resilience_FallbackProvider(t *testing.T) {
	t.Parallel()
	real := newTestProvider(t)
	alwaysFail := newFailNProvider(real, 1000)
	fb := fallback.New(alwaysFail, real)

	a, err := agent.New(fb, "You are a helpful assistant. Be very brief.")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := a.Invoke(agent.NewContext(ctx), "What is the capital of France? Reply with just the city name.")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Text), "paris") {
		t.Errorf("expected response to mention Paris, got: %s", result.Text)
	}
	t.Logf("Fallback succeeded, response: %s", result.Text)
}

func TestIntegration_Resilience_FallbackAllFail(t *testing.T) {
	t.Parallel()
	real := newTestProvider(t)
	fb := fallback.New(newFailNProvider(real, 1000), newFailNProvider(real, 1000))

	a, err := agent.New(fb, "You are a helpful assistant.")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = a.Invoke(agent.NewContext(ctx), "Hello")
	if err == nil {
		t.Fatal("expected error when all fallback providers fail, got nil")
	}
	if !strings.Contains(err.Error(), "all providers failed") {
		t.Logf("Error (may be wrapped): %v", err)
	}
	t.Logf("All fallbacks failed as expected: %v", err)
}

func TestIntegration_Resilience_RetryWithFallback(t *testing.T) {
	t.Parallel()
	real := newTestProvider(t)
	fb := fallback.New(newFailNProvider(real, 1000), real)

	a, err := agent.New(fb, "You are a helpful assistant. Be very brief.")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := a.Invoke(agent.NewContext(ctx), "Say hello in one word.")
	if err != nil {
		t.Fatalf("expected fallback to handle failure, got: %v", err)
	}
	if result.Text == "" {
		t.Error("expected non-empty response")
	}
	t.Logf("Retry+fallback response: %s", result.Text)
}
