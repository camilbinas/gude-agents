# Release support matrix

This matrix is the release-qualification contract for the framework. It describes what is exercised by deterministic local checks, not an upstream provider/model availability guarantee. Per-model capabilities remain configuration-specific; see [Providers](providers.md).

| Area | Status | Release qualification and boundary |
|---|---|---|
| In-process agent loop, tool schema validation, filters, policy enforcement, approvals, resume, and stream ordering | Supported | Covered by deterministic local Go tests. Tool policy is enforced at both advertisement and execution; a declared role/attribute policy requires a `Principal`. |
| Conversation and execution contracts | Supported | The interfaces and in-memory/local conformance behavior are exercised locally. Deployments must validate their selected store's credentials, network, migrations, durability, and CAS behavior. |
| Built-in provider adapters | Experimental | Unit tests cover adapter behavior without paid calls. Model availability, API behavior, credentials, quotas, regions, and network reachability are environment-dependent and are not release-qualified here. |
| External conversation/ratelimit backends (Postgres, Redis, DynamoDB) | Experimental | Redis lease accounting is exercised with miniredis for atomic reserve/commit/release and lifecycle retention. Source-level/local tests do not substitute for a provisioned service, IAM/auth, TLS, migration, retention, and failure-mode validation in the target environment. |
| Background tools | Local-only | Detached background execution is a local framework mechanism. It is not a distributed worker, scheduler, lease/ownership protocol, or cross-process delivery guarantee. |
| Durable tool recovery | Supported with idempotent downstream operations | Recovery preserves an idempotency key and replays only tools declared replay-safe. Unsafe in-flight outcomes require reconciliation. |
| Exactly-once external execution | Unsupported | The framework cannot prove an external side effect occurred exactly once. Downstream services must provide idempotency and operators must reconcile uncertain outcomes. |
| Provider-response replay, paid-provider qualification, and production credential validation | Unsupported in this release process | These require provider accounts and target-environment controls and are deliberately excluded from deterministic local release checks. |

## Environment gaps to close before deployment

| Integration | Required deployment qualification |
|---|---|
| Provider | Supply the adapter's credentials and endpoint/model configuration, then verify the chosen model's tool, streaming, timeout, quota, and region behavior. No paid call is part of release qualification. |
| HTTP/network | Verify egress, DNS, proxy, TLS roots, request-size limits, cancellation propagation, and retry behavior against the target provider. |
| Durable backend | Provision and test credentials/IAM, encryption/TLS, schema or table setup, retention, concurrency/CAS conflicts, backups, and outage recovery. |
| Background work | Provide application-owned scheduling, ownership, retry/dead-letter, monitoring, and idempotent side-effect controls if work must survive a process boundary. |

## Retry classification status

The current provider contract returns ordinary Go errors and has no shared typed API error carrying HTTP status, provider code, or retry-after. The engine preserves the no-visible-stream retry invariant, but its existing silent-error retry behavior cannot distinguish a transient provider failure from authentication, schema, filter, or another permanent error. A safe transient-only classifier would require adapters to return a provider-neutral typed error. That broader public/API redesign is proposed rather than implemented in this release; the proposed classifier must not retry generic authentication, schema, filter, or arbitrary provider errors.

Deterministic scenarios used for release qualification are versioned in [evaluation-cases-v1.md](evaluation-cases-v1.md).
