package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// WithProviderTimeout tests
// ---------------------------------------------------------------------------

// slowProvider blocks for the given duration before responding.
type slowProvider struct {
	delay    time.Duration
	response *ModelResponse
}

func (p *slowProvider) Name() string { return "mock" }

func (p *slowProvider) Stream(ctx context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	select {
	case <-time.After(p.delay):
		if emit != nil && p.response.Text != "" {
			emit(ModelEvent{Type: ModelEventText, Text: p.response.Text})
		}
		return p.response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestWithProviderTimeout_ProviderRespondsInTime(t *testing.T) {
	sp := &slowProvider{
		delay:    10 * time.Millisecond,
		response: &ModelResponse{Text: "fast"},
	}
	a, err := New(sp, "sys", WithProviderTimeout(1*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "hi")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if result.Text != "fast" {
		t.Errorf("expected %q, got %q", "fast", result.Text)
	}
}

func TestWithProviderTimeout_ProviderTimesOut(t *testing.T) {
	sp := &slowProvider{
		delay:    5 * time.Second,
		response: &ModelResponse{Text: "slow"},
	}
	a, err := New(sp, "sys", WithProviderTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hi")
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

func TestWithProviderTimeout_ZeroMeansNoTimeout(t *testing.T) {
	sp := &slowProvider{
		delay:    10 * time.Millisecond,
		response: &ModelResponse{Text: "ok"},
	}
	a, err := New(sp, "sys", WithProviderTimeout(0))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "hi")
	if err != nil {
		t.Fatalf("expected success with zero timeout, got: %v", err)
	}
	if result.Text != "ok" {
		t.Errorf("expected %q, got %q", "ok", result.Text)
	}
}

func TestWithProviderTimeout_NegativeReturnsError(t *testing.T) {
	_, err := New(mockProvider{}, "sys", WithProviderTimeout(-1*time.Second))
	if err == nil {
		t.Fatal("expected error for negative timeout")
	}
}

// ---------------------------------------------------------------------------
// WithProviderRetry tests
// ---------------------------------------------------------------------------

// failNProvider fails the first N calls, then succeeds.
type failNProvider struct {
	failCount int
	calls     atomic.Int32
	response  *ModelResponse
}

func (p *failNProvider) Name() string { return "mock" }

func (p *failNProvider) Stream(_ context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	n := int(p.calls.Add(1))
	if n <= p.failCount {
		return nil, fmt.Errorf("transient error (call %d)", n)
	}
	if emit != nil && p.response.Text != "" {
		emit(ModelEvent{Type: ModelEventText, Text: p.response.Text})
	}
	return p.response, nil
}

func TestWithProviderRetry_SucceedsAfterTransientFailure(t *testing.T) {
	fp := &failNProvider{
		failCount: 2,
		response:  &ModelResponse{Text: "recovered"},
	}
	a, err := New(fp, "sys",
		WithProviderRetry(3, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "hi")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if result.Text != "recovered" {
		t.Errorf("expected %q, got %q", "recovered", result.Text)
	}
	if calls := int(fp.calls.Load()); calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", calls)
	}
}

func TestWithProviderRetry_ExhaustsRetries(t *testing.T) {
	fp := &failNProvider{
		failCount: 10, // always fails
		response:  &ModelResponse{Text: "never"},
	}
	a, err := New(fp, "sys",
		WithProviderRetry(2, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hi")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// Should have made 3 attempts (1 initial + 2 retries).
	if calls := int(fp.calls.Load()); calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

func TestWithProviderRetry_ZeroMeansNoRetry(t *testing.T) {
	fp := &failNProvider{
		failCount: 1,
		response:  &ModelResponse{Text: "ok"},
	}
	a, err := New(fp, "sys",
		WithProviderRetry(0, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hi")
	if err == nil {
		t.Fatal("expected error with zero retries")
	}
	if calls := int(fp.calls.Load()); calls != 1 {
		t.Errorf("expected 1 call with zero retries, got %d", calls)
	}
}

func TestWithProviderRetry_RespectsContextCancellation(t *testing.T) {
	fp := &failNProvider{
		failCount: 10,
		response:  &ModelResponse{Text: "never"},
	}
	a, err := New(fp, "sys",
		WithProviderRetry(5, 500*time.Millisecond), // long delay
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = a.Invoke(NewContext(ctx), "hi")
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	// Should have stopped early, not made all 6 attempts.
	if calls := int(fp.calls.Load()); calls > 2 {
		t.Errorf("expected early stop from context cancellation, got %d calls", calls)
	}
}

func TestWithProviderRetry_NegativeReturnsError(t *testing.T) {
	_, err := New(mockProvider{}, "sys", WithProviderRetry(-1, time.Second))
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
		fn: func(ctx context.Context, params ModelRequest, cb func(ModelEvent)) (*ModelResponse, error) {
			n := int(calls.Add(1))
			if n <= 2 {
				// Slow — will be killed by timeout.
				select {
				case <-time.After(5 * time.Second):
					return &ModelResponse{Text: "slow"}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &ModelResponse{Text: "fast"}, nil
		},
	}

	a, err := New(sp, "sys",
		WithProviderTimeout(50*time.Millisecond),
		WithProviderRetry(3, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "hi")
	if err != nil {
		t.Fatalf("expected success after timeout+retry, got: %v", err)
	}
	if result.Text != "fast" {
		t.Errorf("expected %q, got %q", "fast", result.Text)
	}
	if c := int(calls.Load()); c != 3 {
		t.Errorf("expected 3 calls, got %d", c)
	}
}

func TestWithProviderRetry_DoesNotRetryAfterVisibleStreamOutput(t *testing.T) {
	partialErr := errors.New("stream interrupted after partial output")
	calls := 0
	provider := &funcProvider{fn: func(_ context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
		calls++
		if calls == 1 {
			emit(ModelEvent{Type: ModelEventText, Text: "partial"})
			return nil, partialErr
		}
		return &ModelResponse{Text: "replacement"}, nil
	}}
	a, err := New(provider, "sys", WithProviderRetry(2, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	var chunks []string
	for chunk, serr := range a.TextStream(Background(), "hi") {
		if serr != nil {
			err = serr
			break
		}
		chunks = append(chunks, chunk)
	}
	if !errors.Is(err, partialErr) {
		t.Fatalf("TextStream error = %v, want partial stream error", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1 after visible output", calls)
	}
	if len(chunks) != 1 || chunks[0] != "partial" {
		t.Fatalf("visible chunks = %#v, want only partial", chunks)
	}
}

func TestWithProviderRetry_DoesNotRetryAfterThinkingEvent(t *testing.T) {
	thinkingErr := errors.New("stream interrupted after thinking")
	calls := 0
	provider := &funcProvider{fn: func(_ context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
		calls++
		emit(ModelEvent{Type: ModelEventThinking, Text: "reasoning"})
		return nil, thinkingErr
	}}
	a, err := New(provider, "sys", WithProviderRetry(2, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	events, streamErr := collectStream(a.Stream(Background(), "hi"))
	if !errors.Is(streamErr, thinkingErr) {
		t.Fatalf("Stream error = %v, want thinking stream error", streamErr)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1 after thinking event", calls)
	}
	if len(events) < 2 || events[1].Type != EventThinking || events[1].Thinking == nil || events[1].Thinking.Content != "reasoning" {
		t.Fatalf("events = %#v, want reasoning thinking event", events)
	}
}
