// Package ratelimit implements provider-call RPM, TPM, and process-local
// concurrency limiting. Attach a *ratelimit.RateLimiter to an agent with
// agent.WithRateLimiter.
package ratelimit

import agent "github.com/camilbinas/gude-agents/agent"

// Root request and usage types are aliases so rate limiting can estimate and
// reconcile provider calls without duplicating their data contract.
type ModelRequest = agent.ModelRequest
type ModelEvent = agent.ModelEvent
type ModelResponse = agent.ModelResponse
type TokenUsage = agent.TokenUsage
type TokenEstimator = agent.TokenEstimator
type CharEstimator = agent.CharEstimator

const ModelEventText = agent.ModelEventText

var ErrRateLimitExceeded = agent.ErrRateLimitExceeded
