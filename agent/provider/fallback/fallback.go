// Package fallback provides a Provider that tries a primary provider and
// automatically retries with one or more fallback providers on error.
//
// Usage:
//
//	primary, _ := anthropic.New(anthropic.Claude37Sonnet)
//	backup, _  := bedrock.ClaudeSonnet4_6()
//	p := fallback.New(primary, backup)
//
// An error from the primary causes an immediate retry on the next provider only
// if no model event has reached the caller. The first successful response is
// returned. If all providers fail, the last error is returned.
package fallback

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/camilbinas/gude-agents/agent"
)

// Provider wraps a chain of providers and falls back to the next one on error.
type Provider struct {
	chain []agent.Provider
}

// New creates a fallback Provider. primary is tried first; each subsequent
// provider in fallbacks is tried in order if the previous one fails before
// emitting an event to the caller.
func New(primary agent.Provider, fallbacks ...agent.Provider) *Provider {
	chain := make([]agent.Provider, 0, 1+len(fallbacks))
	chain = append(chain, primary)
	chain = append(chain, fallbacks...)
	return &Provider{chain: chain}
}

// Name returns a human-readable identifier for this provider instance.
func (p *Provider) Name() string { return "fallback" }

// Stream tries each provider in order, returning the first success. A failed
// provider is retried only when it has not emitted an event to the caller.
func (p *Provider) Stream(ctx context.Context, req agent.ModelRequest, emit func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	var lastErr error
	for i, provider := range p.chain {
		var emitted atomic.Bool
		attemptEmit := emit
		if emit != nil {
			attemptEmit = func(event agent.ModelEvent) {
				emitted.Store(true)
				emit(event)
			}
		}

		resp, err := provider.Stream(ctx, req, attemptEmit)
		if err == nil {
			return resp, nil
		}

		lastErr = fmt.Errorf("provider[%d]: %w", i, err)
		if emitted.Load() {
			return nil, lastErr
		}
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}
