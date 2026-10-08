package agent

import "context"

// RateLimitRequest describes one physical provider attempt. Key scopes the
// per-customer budget; Request is the exact provider request being dispatched.
type RateLimitRequest struct {
	Key     string
	Request ModelRequest
}

// RateLimitLease is an opaque reservation returned by a RateLimiter.
type RateLimitLease interface{ RateLimitLease() }

// RateLimiter is the engine-facing provider-attempt accounting contract.
// Reserve admits one attempt, Commit reconciles confirmed usage, and Release
// conservatively settles an attempt whose provider usage is uncertain.
type RateLimiter interface {
	Reserve(context.Context, RateLimitRequest) (RateLimitLease, error)
	Commit(context.Context, RateLimitLease, TokenUsage) error
	Release(context.Context, RateLimitLease) error
}
