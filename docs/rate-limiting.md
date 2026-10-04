# Rate limiting

`RateLimiter` controls provider-call admission. Attach one to an agent with `agent.WithRateLimiter`:

```go
import (
    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/ratelimit"
)

limiter, err := ratelimit.NewRateLimiter(
    ratelimit.RPM(60),
    ratelimit.TPM(100_000),
    ratelimit.MaxConcurrent(4),
)
a, err := agent.New(provider, "You are helpful.", agent.WithRateLimiter(limiter))
```

## Lease lifecycle

Every real provider attempt acquires a rate-limit lease before dispatch. The lease owns:

- an RPM reservation;
- an estimated TPM reservation when a token estimator is available;
- a process-local `MaxConcurrent` slot.

A successful attempt reconciles its estimate to actual `TokenUsage`. A smaller actual value releases excess token capacity; a larger actual value is recorded as reality, even if it temporarily pushes usage above the configured TPM limit. Later admissions are blocked until capacity expires.

A failed or ambiguous provider attempt conservatively finalizes its estimated TPM reservation. This may over-count a failed request, but it does not silently under-count possibly consumed provider tokens. Every retry is a new provider attempt and receives a new lease.

Estimator failures fail open for TPM reservation: RPM and concurrency still apply, while token reservation is skipped. `WithoutTokenReservation()` explicitly disables estimated TPM reservation; `WithoutPreFlight()` remains an alias for compatibility.

## Limits and scopes

- **RPM** is a hard request-admission reservation.
- **TPM** reserves estimated tokens at admission, then reconciles to actual provider usage.
- **MaxConcurrent** is process-local, including when `WithStore` is configured. It is not a distributed semaphore.
- Per-key and global limits are additive. A call must reserve every applicable counter or none.

`WithBlock()` waits for admission capacity; `WithFailFast()` returns `agent.ErrRateLimitExceeded` immediately. Cancellation before admission leaves no reservation or concurrency slot. Accounting after a provider attempt uses a bounded context that survives caller cancellation.

## Distributed stores

`WithStore` distributes RPM and TPM lease accounting, not concurrency. The Redis store uses Redis server time and atomically reserves per-key plus global lease counters in one hash slot. Redis currently supports sliding windows for lease reservations; requesting `WithFixedWindow()` with that store fails explicitly rather than silently changing strategy.

Token estimates are intentionally advisory. Explicit provider or request output limits remain provider-owned configuration; rate-limit token reservations govern future admission and do not override model settings.
