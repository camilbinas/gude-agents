# Rate limiting

Attach a `*ratelimit.RateLimiter` with `agent.WithRateLimiter`. The engine uses one lease-only contract for every physical provider attempt:

```go
limiter, err := ratelimit.NewRateLimiter(
    ratelimit.RPM(60),
    ratelimit.TPM(100_000),
    ratelimit.MaxConcurrent(4),
)
a, err := agent.New(provider, "You are helpful.", agent.WithRateLimiter(limiter))
```

## v1 lease API

The engine-facing `agent.RateLimiter` exposes only `Reserve(ctx, request)`, `Commit(ctx, lease, actualUsage)`, and `Release(ctx, lease)`. A lease is opaque. `Commit` records confirmed actual usage; `Release` terminally and conservatively settles the original reservation when dispatch may have happened but usage is unknown. Both return lifecycle errors detectable with `errors.Is`: `agent.ErrRateLimitLeaseTerminal`, `agent.ErrRateLimitLeaseCrossTerminal`, `agent.ErrRateLimitLeaseUnknown`, and `agent.ErrRateLimitLeaseExpired`.

Reserve happens once per provider attempt, including retries. A successful attempt commits actual usage. Any provider error or cancellation after dispatch releases the reservation conservatively; it is not automatically refunded. The engine never retries after visible stream output. A cancellation observed before provider dispatch results in no provider call; callers that can prove no dispatch should avoid reserving that attempt.

## Cost and scope

RPM is charged at reservation. TPM reserves the request estimator plus the configured `InferenceConfig.MaxTokens` output bound when present, then commits actual usage. If `MaxTokens` is absent, the documented default output fallback is zero because no provider-independent maximum is known; configure `WithOutputReservationFallback` when an application has a safe bound. `WithoutTokenReservation` disables estimated TPM reservations only.

Per-key and global counters are atomic and additive. `MaxConcurrent` is process-local even with Redis. `WithFailFast` returns `agent.ErrRateLimitExceeded`; `WithBlock` waits for capacity or cancellation.

## Lease retention and backends

`MemoryStore` and the Redis store are lease-only backends. `WithPendingLeaseTTL` / `WithTerminalLeaseTTL` configure MemoryStore retention; Redis exposes the equivalent `redis.WithLeaseTTLs`. Values must be positive. Effective pending retention is at least twice the largest rate window, so it outlives the window and cannot silently refund abandoned capacity. When a pending lease expires, its estimate is conservatively settled and late terminal operations return `ErrRateLimitLeaseExpired`. Terminal records remain for at least the relevant counter window to detect duplicate and cross-terminal operations.

Redis uses server time and a same-slot Lua transaction for atomic per-key/global reserve, commit, and release. It supports sliding windows. Raw script status values never escape the backend; callers receive the typed lifecycle errors above.

## Migration

This is a breaking v1 removal. Replace all direct admission/usage bookkeeping with an `agent.RateLimiter` implementation and pass it through `agent.WithRateLimiter`. Do not retain legacy acquire/record or separate check APIs; they no longer exist. See the support matrix and evaluation cases for the tested retention, retry, and uncertain-dispatch boundaries.
