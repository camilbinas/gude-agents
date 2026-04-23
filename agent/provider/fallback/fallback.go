// Package fallback provides a Provider that tries a primary provider and
// automatically retries with one or more fallback providers on error.
//
// Usage:
//
//	primary, _ := anthropic.New(anthropic.Claude37Sonnet)
//	backup, _  := bedrock.ClaudeSonnet4_6()
//	p := fallback.New(primary, backup)
//
// Any error from the primary causes an immediate retry on the next provider
// in the chain. The first successful response is returned. If all providers
// fail, the last error is returned.
package fallback

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

// Provider wraps a chain of providers and falls back to the next one on error.
type Provider struct {
	chain []agent.Provider
}

// New creates a fallback Provider. primary is tried first; each subsequent
// provider in fallbacks is tried in order if the previous one fails.
func New(primary agent.Provider, fallbacks ...agent.Provider) *Provider {
	chain := make([]agent.Provider, 0, 1+len(fallbacks))
	chain = append(chain, primary)
	chain = append(chain, fallbacks...)
	return &Provider{chain: chain}
}

// Converse tries each provider in order, returning the first success.
func (p *Provider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	var lastErr error
	for i, provider := range p.chain {
		resp, err := provider.Converse(ctx, params)
		if err == nil {
			return resp, nil
		}
		lastErr = fmt.Errorf("provider[%d]: %w", i, err)
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

// ConverseStream tries each provider in order, returning the first success.
// Note: if the primary fails mid-stream, the fallback starts a fresh request.
func (p *Provider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	var lastErr error
	for i, provider := range p.chain {
		resp, err := provider.ConverseStream(ctx, params, cb)
		if err == nil {
			return resp, nil
		}
		lastErr = fmt.Errorf("provider[%d]: %w", i, err)
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}
