// Package ratelimit implements provider-attempt RPM, TPM, and process-local
// concurrency limiting. Attach a *ratelimit.RateLimiter with agent.WithRateLimiter.
package ratelimit

import agent "github.com/camilbinas/gude-agents/agent"

// Root types are aliases so the limiter has one provider data contract.
type ModelRequest = agent.ModelRequest
type ModelEvent = agent.ModelEvent
type ModelResponse = agent.ModelResponse
type TokenUsage = agent.TokenUsage
type TokenEstimator = agent.TokenEstimator
type CharEstimator = agent.CharEstimator
type Request = agent.RateLimitRequest

const ModelEventText = agent.ModelEventText

var (
	ErrRateLimitExceeded  = agent.ErrRateLimitExceeded
	ErrLeaseTerminal      = agent.ErrRateLimitLeaseTerminal
	ErrLeaseCrossTerminal = agent.ErrRateLimitLeaseCrossTerminal
	ErrLeaseUnknown       = agent.ErrRateLimitLeaseUnknown
	ErrLeaseExpired       = agent.ErrRateLimitLeaseExpired
)
