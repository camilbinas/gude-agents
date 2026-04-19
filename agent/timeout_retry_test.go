package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

// ---------------------------------------------------------------------------
// WithTimeout tests
// ---------------------------------------------------------------------------

// slowProvider blocks for the given duration before responding.
type slowProvider struct {
	delay    time.Duration
	response *ProviderResponse
}

func (p *slowProvider) Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error) {
	select {
	case <-time.After(p.delay):
		return p.response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *slowProvider) ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	select {
	case <-time.After(p.delay):
		if cb != nil && p.response.Text != "" {
			cb(p.response.Text)
		}
		return p.response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestWithTimeout_ProviderRespondsInTime(t *testing.T) {
	sp := &slowProvider{
		delay:    10 * time.Millisecond,
		response: &ProviderResponse{Text: "fast"},
	}
	a, err := New(sp, prompt.Text("sys"), nil, WithTimeout(1*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if result != "fast" {
		t.Errorf("expected %q, got %q", "fast", result)
	}
}

func TestWithTimeout_ProviderTimesOut(t *testing.T) {
	sp := &slowProvider{
		delay:    5 * time.Second,
		response: &ProviderResponse{Text: "slow"},
	}
	a, err := New(sp, prompt.Text("sys"), nil, WithTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	// Should be a ProviderError wrapping context.DeadlineExceeded.
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ProviderError, got %T: %v", err, err)
	}
	if !errors.Is(pe.Cause, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded cause, got: %v", pe.Cause)
	}
}

func TestWithTimeout_ZeroMeansNoTimeout(t *testing.T) {
	sp := &slowProvider{
		delay:    10 * time.Millisecond,
		response: &ProviderResponse{Text: "ok"},
	}
	a, err := New(sp, prompt.Text("sys"), nil, WithTimeout(0))
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("expected success with zero timeout, got: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected %q, got %q", "ok", result)
	}
}

func TestWithTimeout_NegativeReturnsError(t *testing.T) {
	_, err := New(mockProvider{}, prompt.Text("sys"), nil, WithTimeout(-1*time.Second))
	if err == nil {
		t.Fatal("expected error for negative timeout")
	}
}

// ---------------------------------------------------------------------------
// WithRetry tests
// ---------------------------------------------------------------------------

// failNProvider fails the first N calls, then succeeds.
type failNProvider struct {
	failCount int
	calls     atomic.Int32
	response  *ProviderResponse
}

func (p *failNProvider) Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error) {
	return p.ConverseStream(ctx, params, nil)
}

func (p *failNProvider) ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	n := int(p.calls.Add(1))
	if n <= p.failCount {
		return nil, fmt.Errorf("transient error (call %d)", n)
	}
	if cb != nil && p.response.Text != "" {
		cb(p.response.Text)
	}
	return p.response, nil
}

func TestWithRetry_SucceedsAfterTransientFailure(t *testing.T) {
	fp := &failNProvider{
		failCount: 2,
		response:  &ProviderResponse{Text: "recovered"},
	}
	a, err := New(fp, prompt.Text("sys"), nil,
		WithRetry(3, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if result != "recovered" {
		t.Errorf("expected %q, got %q", "recovered", result)
	}
	if calls := int(fp.calls.Load()); calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", calls)
	}
}

func TestWithRetry_ExhaustsRetries(t *testing.T) {
	fp := &failNProvider{
		failCount: 10, // always fails
		response:  &ProviderResponse{Text: "never"},
	}
	a, err := New(fp, prompt.Text("sys"), nil,
		WithRetry(2, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// Should have made 3 attempts (1 initial + 2 retries).
	if calls := int(fp.calls.Load()); calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

func TestWithRetry_ZeroMeansNoRetry(t *testing.T) {
	fp := &failNProvider{
		failCount: 1,
		response:  &ProviderResponse{Text: "ok"},
	}
	a, err := New(fp, prompt.Text("sys"), nil,
		WithRetry(0, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error with zero retries")
	}
	if calls := int(fp.calls.Load()); calls != 1 {
		t.Errorf("expected 1 call with zero retries, got %d", calls)
	}
}

func TestWithRetry_RespectsContextCancellation(t *testing.T) {
	fp := &failNProvider{
		failCount: 10,
		response:  &ProviderResponse{Text: "never"},
	}
	a, err := New(fp, prompt.Text("sys"), nil,
		WithRetry(5, 500*time.Millisecond), // long delay
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err = a.Invoke(ctx, "hi")
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	// Should have stopped early, not made all 6 attempts.
	if calls := int(fp.calls.Load()); calls > 2 {
		t.Errorf("expected early stop from context cancellation, got %d calls", calls)
	}
}

func TestWithRetry_NegativeReturnsError(t *testing.T) {
	_, err := New(mockProvider{}, prompt.Text("sys"), nil, WithRetry(-1, time.Second))
	if err == nil {
		t.Fatal("expected error for negative maxRetries")
	}
}

// ---------------------------------------------------------------------------
// Combined timeout + retry
// ---------------------------------------------------------------------------

func TestTimeoutAndRetry_Combined(t *testing.T) {
	// Provider is slow on first 2 calls (triggers timeout), fast on 3rd.
	var calls atomic.Int32
	sp := &funcProvider{
		fn: func(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
			n := int(calls.Add(1))
			if n <= 2 {
				// Slow — will be killed by timeout.
				select {
				case <-time.After(5 * time.Second):
					return &ProviderResponse{Text: "slow"}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &ProviderResponse{Text: "fast"}, nil
		},
	}

	a, err := New(sp, prompt.Text("sys"), nil,
		WithTimeout(50*time.Millisecond),
		WithRetry(3, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("expected success after timeout+retry, got: %v", err)
	}
	if result != "fast" {
		t.Errorf("expected %q, got %q", "fast", result)
	}
	if c := int(calls.Load()); c != 3 {
		t.Errorf("expected 3 calls, got %d", c)
	}
}
