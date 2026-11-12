// Package testutil provides supported, public test helpers for building agents
// in tests: MockProvider (a configurable agent.Provider) and the property-based
// message/content generators. These are part of the module's public API and
// follow its compatibility guarantees; depend on them from your own test
// suites.
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

// MockProvider is a configurable, concurrency-safe test implementation of
// agent.Provider (and agent.ModelIdentifier). It is supported public API for
// use in downstream test suites, covering the common patterns needed across
// agent unit tests: scripted responses, request capture, streaming, delays, and
// error injection. Configure via WithResponses, WithCapture, WithError,
// WithDelay, WithFailFirst, WithStreamWords, and WithModelID.
//
// Scripted responses are returned verbatim, including their StopReason and
// RawStopReason. When a scripted response leaves StopReason unset
// (ModelStopUnknown), Stream fills in a sensible default so the response looks
// like a real provider reply: ModelStopToolUse when it carries tool calls,
// otherwise ModelStopEndTurn. Set StopReason explicitly on a response to
// exercise other stop reasons (max_tokens, content_filter, ...).
type MockProvider struct {
	mu        sync.Mutex
	responses []*agent.ModelResponse
	callIndex int

	// capture
	capture  bool
	captured []agent.ModelRequest

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
func WithResponses(responses ...*agent.ModelResponse) MockProviderOption {
	return func(p *MockProvider) {
		p.responses = append(p.responses, responses...)
	}
}

// WithCapture enables recording of all ModelRequests received by the provider.
// Access recorded requests via Provider.Captured().
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
// context cancellation, so it works correctly with timeout tests.
func WithDelay(d time.Duration) MockProviderOption {
	return func(p *MockProvider) { p.delay = d }
}

// WithStreamWords enables word-level streaming for text responses. When set,
// Stream emits text as individual word chunks. Default is to emit the full text
// as a single event.
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

// Stream implements agent.Provider.
func (p *MockProvider) Stream(ctx context.Context, req agent.ModelRequest, emit func(agent.ModelEvent)) (*agent.ModelResponse, error) {
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

	// Capture the request if requested.
	if p.capture {
		p.captured = append(p.captured, req)
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

	// Default the stop reason so the mock looks like a real provider reply,
	// without mutating the caller's scripted response (it may be reused).
	if resp != nil && resp.StopReason == agent.ModelStopUnknown {
		defaulted := *resp
		if len(defaulted.ToolCalls) > 0 {
			defaulted.StopReason = agent.ModelStopToolUse
		} else {
			defaulted.StopReason = agent.ModelStopEndTurn
		}
		resp = &defaulted
	}

	// Stream text via model events.
	if emit != nil && len(resp.ToolCalls) == 0 && resp.Text != "" {
		if p.streamWords {
			words := strings.Fields(resp.Text)
			for i, word := range words {
				if i > 0 {
					emit(agent.ModelEvent{Type: agent.ModelEventText, Text: " "})
				}
				emit(agent.ModelEvent{Type: agent.ModelEventText, Text: word})
			}
		} else {
			emit(agent.ModelEvent{Type: agent.ModelEventText, Text: resp.Text})
		}
	}

	return resp, nil
}

// Name implements agent.Provider.
func (p *MockProvider) Name() string { return "mock" }

// ModelID implements agent.ModelIdentifier.
func (p *MockProvider) ModelID() string { return p.modelID }

// Captured returns all ModelRequests received since the provider was created.
// Only populated when WithCapture() is set.
func (p *MockProvider) Captured() []agent.ModelRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]agent.ModelRequest, len(p.captured))
	copy(out, p.captured)
	return out
}

// CallCount returns the number of successful scripted responses consumed.
func (p *MockProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callIndex
}

// Reset resets the call index and captured requests, allowing the provider to
// be reused across test cases.
func (p *MockProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callIndex = 0
	p.captured = nil
	p.failCount.Store(0)
}
