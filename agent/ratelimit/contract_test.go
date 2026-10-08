package ratelimit

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/camilbinas/gude-agents/agent"
)

type fixedEstimator struct{ n int }

func (e fixedEstimator) EstimateTokens(context.Context, ModelRequest) (int, error) { return e.n, nil }
func limiterForTest(t *testing.T, opts ...RateLimiterOption) *RateLimiter {
	t.Helper()
	r, err := NewRateLimiter(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func reserveForTest(t *testing.T, r *RateLimiter, key string, maxTokens int) agent.RateLimitLease {
	t.Helper()
	req := agent.RateLimitRequest{Key: key}
	if maxTokens > 0 {
		req.Request.InferenceConfig = &agent.InferenceConfig{MaxTokens: &maxTokens}
	}
	lease, err := r.Reserve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func memoryStore(t *testing.T, opts ...StoreOption) *MemoryStore {
	t.Helper()
	store, err := NewMemoryStore(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestLeaseContractMemory(t *testing.T) {
	if _, err := NewMemoryStore(WithPendingLeaseTTL(0)); err == nil {
		t.Fatal("zero configured pending TTL must fail validation")
	}
	for _, store := range []Store{memoryStore(t)} {
		r := limiterForTest(t, RPM(2), TPM(100), WithTokenEstimator(fixedEstimator{n: 20}), WithStore(store))
		first := reserveForTest(t, r, "customer", 0)
		if err := r.Commit(context.Background(), first, TokenUsage{InputTokens: 10}); err != nil {
			t.Fatal(err)
		}
		if err := r.Commit(context.Background(), first, TokenUsage{InputTokens: 10}); !errors.Is(err, agent.ErrRateLimitLeaseTerminal) {
			t.Fatalf("double commit = %v", err)
		}
		if err := r.Release(context.Background(), first); !errors.Is(err, agent.ErrRateLimitLeaseCrossTerminal) {
			t.Fatalf("cross terminal = %v", err)
		}
		second := reserveForTest(t, r, "customer", 0)
		if err := r.Release(context.Background(), second); err != nil {
			t.Fatal(err)
		}
		if err := r.Release(context.Background(), second); !errors.Is(err, agent.ErrRateLimitLeaseTerminal) {
			t.Fatalf("double release = %v", err)
		}
		if err := r.Commit(context.Background(), second, TokenUsage{}); !errors.Is(err, agent.ErrRateLimitLeaseCrossTerminal) {
			t.Fatalf("release then commit = %v", err)
		}
		foreign := limiterForTest(t, RPM(1))
		if err := foreign.Release(context.Background(), second); !errors.Is(err, agent.ErrRateLimitLeaseUnknown) {
			t.Fatalf("foreign lease = %v", err)
		}
	}
}

func TestLeaseReservationAccountingAndAtomicGlobalRejection(t *testing.T) {
	r := limiterForTest(t, TPM(100), WithGlobalTPM(15), WithTokenEstimator(fixedEstimator{n: 20}))
	if _, err := r.Reserve(context.Background(), agent.RateLimitRequest{Key: "a"}); err == nil {
		t.Fatal("expected global rejection")
	}
	r = limiterForTest(t, TPM(100), WithTokenEstimator(fixedEstimator{n: 20}))
	lease := reserveForTest(t, r, "a", 0)
	if err := r.Commit(context.Background(), lease, TokenUsage{InputTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reserve(context.Background(), agent.RateLimitRequest{Key: "a"}); err != nil {
		t.Fatalf("actual-lower capacity was not released: %v", err)
	}
	r = limiterForTest(t, TPM(30), WithTokenEstimator(fixedEstimator{n: 10}))
	lease = reserveForTest(t, r, "a", 0)
	if err := r.Commit(context.Background(), lease, TokenUsage{InputTokens: 40}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reserve(context.Background(), agent.RateLimitRequest{Key: "a"}); !errors.Is(err, agent.ErrRateLimitExceeded) {
		t.Fatalf("actual-higher usage was not retained: %v", err)
	}
}

func TestMaxOutputReservationAndCancellation(t *testing.T) {
	r := limiterForTest(t, TPM(30), WithTokenEstimator(fixedEstimator{n: 10}))
	if _, err := r.Reserve(context.Background(), agent.RateLimitRequest{Key: "a", Request: ModelRequest{InferenceConfig: &agent.InferenceConfig{MaxTokens: ptr(25)}}}); !errors.Is(err, agent.ErrRateLimitExceeded) {
		t.Fatalf("estimate plus output bound = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Reserve(ctx, agent.RateLimitRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reserve = %v", err)
	}
}
func ptr(n int) *int { return &n }

func TestMemoryPendingExpirySettlesAndRetainsBucket(t *testing.T) {
	store := memoryStore(t, WithPendingLeaseTTL(time.Millisecond), WithTerminalLeaseTTL(time.Minute))
	now := time.Unix(0, 0)
	store.now = func() time.Time { return now }
	reservation := Reservation{ID: "expired", Tokens: []TokenReservation{{Key: "a", Limit: 10, Window: time.Millisecond, Strategy: SlidingWindow, Amount: 7}}}
	ok, err := store.Reserve(context.Background(), reservation)
	if err != nil || !ok {
		t.Fatalf("reserve = %v,%v", ok, err)
	}
	now = now.Add(3 * time.Millisecond)
	_, _ = store.Reserve(context.Background(), Reservation{ID: "trigger", Tokens: []TokenReservation{{Key: "b", Limit: 10, Window: time.Millisecond, Strategy: SlidingWindow, Amount: 1}}})
	if err := store.Commit(context.Background(), "expired", 1); !errors.Is(err, agent.ErrRateLimitLeaseExpired) {
		t.Fatalf("late terminal = %v", err)
	}
	store.mu.Lock()
	c := store.counters["t:a"]
	total := c.tokenCount()
	store.mu.Unlock()
	if total != 7 {
		t.Fatalf("expired estimate = %d, want 7", total)
	}
}

type retryProvider struct{ calls atomic.Int32 }

func (*retryProvider) Name() string { return "retry" }
func (p *retryProvider) Stream(_ context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	if p.calls.Add(1) == 1 {
		return nil, errors.New("uncertain")
	}
	return &ModelResponse{Text: "ok", Usage: TokenUsage{InputTokens: 10}}, nil
}
func TestRetryUsesOneLeasePerAttempt(t *testing.T) {
	p := &retryProvider{}
	r := limiterForTest(t, TPM(40), WithTokenEstimator(fixedEstimator{n: 20}))
	a, err := agent.New(p, "sys", agent.WithRateLimiter(r), agent.WithProviderRetry(1, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(agent.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 2 {
		t.Fatalf("calls=%d", p.calls.Load())
	}
	if _, err := r.Reserve(context.Background(), agent.RateLimitRequest{}); !errors.Is(err, agent.ErrRateLimitExceeded) {
		t.Fatalf("retry estimate not conservatively retained: %v", err)
	}
}
