# Release support matrix

This matrix is the release-qualification contract for the framework. It describes what is exercised by deterministic local checks, not an upstream provider/model availability guarantee. Per-model capabilities remain configuration-specific; see [Providers](providers.md).

| Area | Status | Release qualification and boundary |
|---|---|---|
| In-process agent loop, tool schema validation, filters, policy enforcement, approvals, resume, and stream ordering | Supported | Covered by deterministic local Go tests. Tool policy is enforced at both advertisement and execution; a declared role/attribute policy requires a `Principal`. |
| `ConversationStore` / `ExecutionStore` interfaces | Supported | The interfaces and the in-memory implementation are exercised by deterministic local conformance tests. Deployments must validate their selected store's credentials, network, migrations, durability, and CAS behavior. |
| Conversation backends | Supported according to the qualification shown here | In-memory and SQLite run the shared append/range/CAS conformance suite locally. Redis append and CAS behavior is exercised against miniredis. PostgreSQL has no automated local conformance and is an optional integration that you must qualify in your own environment. Source-level/local tests do not substitute for a provisioned service, IAM/auth, TLS, migration, retention, and failure-mode validation. |
| `Provider` interface | Supported | Adapters for Anthropic, Amazon Bedrock, OpenAI-compatible endpoints, Ollama, and vLLM are optional integrations. Unit tests cover adapter behavior without paid calls; no adapter is release-qualified against a live service. Model availability, API behavior, credentials, quotas, regions, and network reachability are environment-dependent. |
| RAG, MCP, rate-limit, and observability adapters | Optional integrations | Each is qualified individually by its own package tests. Redis lease accounting is exercised with miniredis for atomic reserve/commit/release and lifecycle retention. |
| Execution stores | Supported according to the qualification shown here | `execution.NewMemory()` is dev/test storage. The PostgreSQL, Redis, and DynamoDB execution stores run one shared conformance suite (create-only, exact-version CAS, concurrent single winner, full-field round trip). Redis is exercised with miniredis and DynamoDB with a faithful fake; PostgreSQL runs against a live server when `POSTGRES_URL` is set. None is qualified against a provisioned cloud service. An execution store is not a conversation store: it holds only unfinished-run state. |
| A2A | Experimental | Not part of the stable v1 compatibility promise. See [A2A](a2a.md). |
| Features removed before v1 | Removed | See the [release notes](release-notes-v1.md#removed-from-the-stable-surface) for the list and migration paths. |
| Durable tool recovery | Supported with idempotent downstream operations | Recovery preserves an idempotency key and replays only tools declared replay-safe. Unsafe in-flight outcomes require reconciliation. |
| Exactly-once external execution | Unsupported | The framework cannot prove an external side effect occurred exactly once. Downstream services must provide idempotency and operators must reconcile uncertain outcomes. |
| Provider-response replay, paid-provider qualification, and production credential validation | Unsupported in this release process | These require provider accounts and target-environment controls and are deliberately excluded from deterministic local release checks. |

## Environment gaps to close before deployment

| Integration | Required deployment qualification |
|---|---|
| Provider | Supply the adapter's credentials and endpoint/model configuration, then verify the chosen model's tool, streaming, timeout, quota, and region behavior. No paid call is part of release qualification. |
| HTTP/network | Verify egress, DNS, proxy, TLS roots, request-size limits, cancellation propagation, and retry behavior against the target provider. |
| Durable backend | Provision and test credentials/IAM, encryption/TLS, schema or table setup, retention, concurrency/CAS conflicts, backups, and outage recovery. |

## Retry classification status

The current provider contract returns ordinary Go errors and has no shared typed API error carrying HTTP status, provider code, or retry-after. The engine preserves the no-visible-stream retry invariant, but its existing silent-error retry behavior cannot distinguish a transient provider failure from authentication, schema, filter, or another permanent error. A safe transient-only classifier would require adapters to return a provider-neutral typed error. That broader public/API redesign is proposed rather than implemented in this release; the proposed classifier must not retry generic authentication, schema, filter, or arbitrary provider errors.

Deterministic scenarios used for release qualification are versioned in [evaluation-cases-v1.md](evaluation-cases-v1.md).
