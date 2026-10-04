package agent

import "context"

// RateLimiter is the engine-facing contract for provider-call admission.
// Concrete policies, stores, and options live in agent/ratelimit so the core
// agent package remains independent of a particular limiter implementation.
type RateLimiter interface {
	AcquireLease(ctx context.Context, key string, req ModelRequest) (RateLimitLease, error)
}

// RateLimitLease owns one provider attempt's rate-limit accounting.
// Commit reconciles actual usage, Fail conservatively finalizes ambiguous
// attempts, and Release frees only process-local execution concurrency.
type RateLimitLease interface {
	Commit(ctx context.Context, usage TokenUsage) error
	Fail(ctx context.Context) error
	Release()
}
