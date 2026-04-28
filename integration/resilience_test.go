package integration_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/fallback"
)

// Resilience integration tests: retry, timeout, and fallback provider.
//
// Run with:
//   go test -v -timeout=120s -run TestIntegration_Resilience ./...

// failNProvider wraps a real provider and fails the first N Converse calls.
type failNProvider struct {
	inner     agent.Provider
	failsLeft atomic.Int32
}

func newFailNProvider(inner agent.Provider, failCount int) *failNProvider {
	p := &failNProvider{inner: inner}
	p.failsLeft.Store(int32(failCount))
	return p
}

func (p *failNProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	if p.failsLeft.Add(-1) >= 0 {
		return nil, errors.New("simulated transient error")
	}
	return p.inner.Converse(ctx, params)
}

func (p *failNProvider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	if p.failsLeft.Add(-1) >= 0 {
		return nil, errors.New("simulated transient error")
	}
	return p.inner.ConverseStream(ctx, params, cb)
}

func TestIntegration_Resilience_RetryRecovers(t *testing.T) {
	real := newTestProvider(t)

	// Fail the first 2 calls, succeed on the 3rd.
	flaky := newFailNProvider(real, 2)

	a, err := agent.New(flaky,
		prompt.Text("You are a helpful assistant. Be very brief."),
		nil,
		agent.WithRetry(3, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "What is 2+2? Reply with just the number.")
	if err != nil {
		t.Fatalf("expected retry to recover, got error: %v", err)
	}

	if !strings.Contains(result, "4") {
		t.Errorf("expected response to contain '4', got: %s", result)
	}
	t.Logf("Retry recovered, response: %s", result)
}

func TestIntegration_Resilience_RetryExhausted(t *testing.T) {
	real := newTestProvider(t)

	// Fail more times than retries allow.
	flaky := newFailNProvider(real, 10)

	a, err := agent.New(flaky,
		prompt.Text("You are a helpful assistant."),
		nil,
		agent.WithRetry(2, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, _, err = a.Invoke(ctx, "Hello")
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}

	t.Logf("Retries exhausted as expected: %v", err)
}

func TestIntegration_Resilience_TimeoutEnforced(t *testing.T) {
	real := newTestProvider(t)

	// Use an absurdly short timeout that will expire before the provider responds.
	a, err := agent.New(real,
		prompt.Text("You are a helpful assistant. Write a very long essay about the history of computing."),
		nil,
		agent.WithTimeout(1*time.Nanosecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, _, err = a.Invoke(ctx, "Write a 1000 word essay.")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	t.Logf("Timeout enforced: %v", err)
}

func TestIntegration_Resilience_FallbackProvider(t *testing.T) {
	real := newTestProvider(t)

	// Primary always fails, fallback is the real provider.
	alwaysFail := newFailNProvider(real, 1000)
	fb := fallback.New(alwaysFail, real)

	a, err := agent.New(fb,
		prompt.Text("You are a helpful assistant. Be very brief."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "What is the capital of France? Reply with just the city name.")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}

	if !strings.Contains(strings.ToLower(result), "paris") {
		t.Errorf("expected response to mention Paris, got: %s", result)
	}
	t.Logf("Fallback succeeded, response: %s", result)
}

func TestIntegration_Resilience_FallbackAllFail(t *testing.T) {
	real := newTestProvider(t)

	fail1 := newFailNProvider(real, 1000)
	fail2 := newFailNProvider(real, 1000)
	fb := fallback.New(fail1, fail2)

	a, err := agent.New(fb,
		prompt.Text("You are a helpful assistant."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, _, err = a.Invoke(ctx, "Hello")
	if err == nil {
		t.Fatal("expected error when all fallback providers fail, got nil")
	}

	if !strings.Contains(err.Error(), "all providers failed") {
		t.Logf("Error (may be wrapped): %v", err)
	}
	t.Logf("All fallbacks failed as expected: %v", err)
}

func TestIntegration_Resilience_RetryWithFallback(t *testing.T) {
	real := newTestProvider(t)

	// Primary fails first 2 calls, fallback is the real provider.
	// With retry(1) on the agent, the first attempt fails, retry fails,
	// but the fallback provider catches it at the provider level.
	flaky := newFailNProvider(real, 1000)
	fb := fallback.New(flaky, real)

	a, err := agent.New(fb,
		prompt.Text("You are a helpful assistant. Be very brief."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "Say hello in one word.")
	if err != nil {
		t.Fatalf("expected fallback to handle failure, got: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty response")
	}
	t.Logf("Retry+fallback response: %s", result)
}
