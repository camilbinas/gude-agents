package testutil

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// MockProvider is a configurable test provider that covers the common patterns
// needed across agent unit tests: scripted responses, param capture, streaming,
// delays, and error injection.
//
// Usage:
//
//	// Simple scripted responses
//	p := testutil.NewProvider(
//	    testutil.WithResponses(&agent.ProviderResponse{Text: "hello"}),
//	)
//
//	// Capture params + scripted responses
//	p := testutil.NewProvider(
//	    testutil.WithResponses(&agent.ProviderResponse{Text: "reply"}),
//	    testutil.WithCapture(),
//	)
//	// After invoke: p.Captured() returns the ConverseParams received
//
//	// Always error
//	p := testutil.NewProvider(testutil.WithError(errors.New("boom")))
//
//	// Delay (for timeout tests)
//	p := testutil.NewProvider(
//	    testutil.WithDelay(500 * time.Millisecond),
//	    testutil.WithResponses(&agent.ProviderResponse{Text: "slow"}),
//	)
//
//	// Fail first N calls, then succeed
//	p := testutil.NewProvider(
//	    testutil.WithFailFirst(2, errors.New("transient")),
//	    testutil.WithResponses(&agent.ProviderResponse{Text: "ok"}),
//	)
type MockProvider struct {
	mu        sync.Mutex
	responses []*agent.ProviderResponse
	callIndex int

	// capture
	capture  bool
	captured []agent.ConverseParams

	// error injection
	fixedErr  error
	failFirst int
	failErr   error
	failCount atomic.Int32

	// delay
	delay time.Duration

	// streaming
	streamWords bool

	// model ID
	modelID string
}

// MockProviderOption configures a Provider.
type MockProviderOption func(*MockProvider)

// WithResponses sets the scripted response queue. Responses are returned in
// order. If the queue is exhausted, subsequent calls return an error.
func WithResponses(responses ...*agent.ProviderResponse) MockProviderOption {
	return func(p *MockProvider) {
		p.responses = append(p.responses, responses...)
	}
}

// WithCapture enables recording of all ConverseParams received by the provider.
// Access recorded params via Provider.Captured().
func WithCapture() MockProviderOption {
	return func(p *MockProvider) { p.capture = true }
}

// WithError makes every call return the given error.
func WithError(err error) MockProviderOption {
	return func(p *MockProvider) { p.fixedErr = err }
}

// WithFailFirst makes the first n calls return err, then falls through to
// the normal response queue.
func WithFailFirst(n int, err error) MockProviderOption {
	return func(p *MockProvider) {
		p.failFirst = n
		p.failErr = err
	}
}

// WithDelay makes each call block for d before responding. The delay respects
// context cancellation, so it works correctly with WithTimeout tests.
func WithDelay(d time.Duration) MockProviderOption {
	return func(p *MockProvider) { p.delay = d }
}

// WithStreamWords enables word-level streaming for text responses. When set,
// ConverseStream delivers text as individual word chunks via the callback.
// Default is to deliver the full text as a single chunk.
func WithStreamWords() MockProviderOption {
	return func(p *MockProvider) { p.streamWords = true }
}

// WithModelID sets the model ID returned by ModelID(). Implements
// agent.ModelIdentifier so the provider can be used in tests that check
// model ID propagation.
func WithModelID(id string) MockProviderOption {
	return func(p *MockProvider) { p.modelID = id }
}

// NewMockProvider creates a configurable test provider.
func NewMockProvider(opts ...MockProviderOption) *MockProvider {
	p := &MockProvider{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Converse implements agent.Provider.
func (p *MockProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	return p.call(ctx, params, nil)
}

// ConverseStream implements agent.Provider.
func (p *MockProvider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	return p.call(ctx, params, cb)
}

// ModelID implements agent.ModelIdentifier.
func (p *MockProvider) ModelID() string { return p.modelID }

// Captured returns all ConverseParams received since the provider was created.
// Only populated when WithCapture() is set.
func (p *MockProvider) Captured() []agent.ConverseParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]agent.ConverseParams, len(p.captured))
	copy(out, p.captured)
	return out
}

// CallCount returns the number of times the provider has been called.
func (p *MockProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callIndex
}

// Reset resets the call index and captured params, allowing the provider to
// be reused across test cases.
func (p *MockProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callIndex = 0
	p.captured = nil
	p.failCount.Store(0)
}

func (p *MockProvider) call(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	// Apply delay (respects context cancellation).
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Capture params if requested.
	if p.capture {
		p.captured = append(p.captured, params)
	}

	// Fixed error — always return this.
	if p.fixedErr != nil {
		return nil, p.fixedErr
	}

	// Fail-first: return error for the first N calls.
	if p.failFirst > 0 {
		n := p.failCount.Add(1)
		if int(n) <= p.failFirst {
			return nil, p.failErr
		}
	}

	// Pop next response.
	if p.callIndex >= len(p.responses) {
		return nil, fmt.Errorf("testutil.Provider: no more responses (call %d)", p.callIndex)
	}
	resp := p.responses[p.callIndex]
	p.callIndex++

	// Stream text via callback.
	if cb != nil && len(resp.ToolCalls) == 0 && resp.Text != "" {
		if p.streamWords {
			words := strings.Fields(resp.Text)
			for i, w := range words {
				if i > 0 {
					cb(" ")
				}
				cb(w)
			}
		} else {
			cb(resp.Text)
		}
	}

	return resp, nil
}
