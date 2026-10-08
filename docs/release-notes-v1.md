# v1 release notes: lease-only rate limiting

Rate limiting is a breaking v1 API migration. The engine-facing contract is now `Reserve(ctx, request)`, `Commit(ctx, lease, usage)`, and `Release(ctx, lease)`. Leases are opaque; terminal calls return typed lifecycle errors that support `errors.Is` for duplicate, conflicting, unknown, and expired leases.

Remove direct request admission, usage recording, independent checks, release callbacks, and legacy store implementations. Provide one `agent.RateLimiter` to `agent.WithRateLimiter`; provider attempts, including eligible retries, are then reserved and terminally accounted for by the engine.

RPM and TPM reserve atomically across per-key/global counters. TPM reserves the request estimate plus `MaxTokens` when configured and reconciles confirmed actual usage. Uncertain provider failures settle the reservation conservatively. Memory and Redis retain pending/terminal ledgers long enough to prevent capacity reset and detect invalid terminal actions; pending expiry settles the estimate and returns the typed expired result.

See [rate limiting](rate-limiting.md), the [support matrix](support-matrix.md), and the versioned [evaluation cases](evaluation-cases-v1.md) for operational boundaries and qualification scenarios.
