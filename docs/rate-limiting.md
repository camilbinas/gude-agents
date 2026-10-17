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

Reserve happens once per provider attempt. The agent loop never retries, so a model call is exactly one attempt. A successful attempt commits actual usage. Any provider error or cancellation after dispatch releases the reservation conservatively; it is not automatically refunded. A cancellation observed before provider dispatch results in no provider call; callers that can prove no dispatch should avoid reserving that attempt.

## Bucket keys

`RateLimitRequest.Key` is an application-defined bucket key. The engine does not derive it from the conversation ID, the identity, or anything else. Choose it with `agent.WithRateLimitKey`, which runs once for every physical provider attempt with that attempt's `*agent.Context`:

```go
limiter, err := ratelimit.NewRateLimiter(
    ratelimit.RPM(60),          // per key
    ratelimit.WithGlobalRPM(600), // across all keys
)
a, err := agent.New(provider, "You are helpful.",
    agent.WithRateLimiter(limiter),
    agent.WithRateLimitKey(func(c *agent.Context) string {
        return c.Identity() // or a tenant ID from c.Principal(), an API key ID, a scope...
    }),
)
```

- **No selector:** every attempt uses the empty key, so all attempts share one per-key bucket. That is a single shared budget, not a per-customer or per-conversation one. There is no fallback from identity to conversation ID.
- **Selector:** attempts that select the same key share a budget regardless of conversation; different keys are independent. Return a stable, bounded set of keys: each key is a bucket in the limiter's store.
- **Global limits** (`WithGlobalRPM`, `WithGlobalTPM`, `WithGlobalRequestLimit`, `WithGlobalTokenLimit`) apply across all keys. Use them to protect the provider quota while per-key limits protect individual tenants.
- `WithRateLimitKey(nil)` and `WithRateLimitKey` without `WithRateLimiter` fail `agent.New`.

## Cost and scope

RPM is charged at reservation. TPM reserves the request estimator plus the configured `InferenceConfig.MaxTokens` output bound when present, then commits actual usage. If `MaxTokens` is absent, the documented default output fallback is zero because no provider-independent maximum is known; configure `WithOutputReservationFallback` when an application has a safe bound. `WithoutTokenReservation` disables estimated TPM reservations only. The built-in `agent.CharEstimator` and the `tokencount/tiktoken` estimator count the system prompt, text, tool-use (ID, name, raw input bytes), tool-result (ID, content), and serialized tool specs. Image and document payloads are not estimated, so requests carrying large binary attachments are under-counted.

Per-key and global counters are atomic and additive. "Per-key" means per application-defined bucket key (see above). `MaxConcurrent` is process-local even with Redis. `WithFailFast` returns `agent.ErrRateLimitExceeded`; `WithBlock` waits for capacity or cancellation.

## Lease retention and backends

`MemoryStore` and the Redis store are lease-only backends. `WithPendingLeaseTTL` / `WithTerminalLeaseTTL` configure MemoryStore retention; Redis exposes the equivalent `redis.WithLeaseTTLs`. Values must be positive. Effective pending retention is at least twice the largest rate window, so it outlives the window and cannot silently refund abandoned capacity. When a pending lease expires, its estimate is conservatively settled and late terminal operations return `ErrRateLimitLeaseExpired`. Terminal records remain for at least the relevant counter window to detect duplicate and cross-terminal operations.

Redis uses server time and a same-slot Lua transaction for atomic per-key/global reserve, commit, and release. It supports sliding windows. Raw script status values never escape the backend; callers receive the typed lifecycle errors above.

## Migration

This is a breaking v1 removal. Replace all direct admission/usage bookkeeping with an `agent.RateLimiter` implementation and pass it through `agent.WithRateLimiter`. Do not retain legacy acquire/record or separate check APIs; they no longer exist. See the support matrix and evaluation cases for the tested retention and uncertain-dispatch boundaries.
