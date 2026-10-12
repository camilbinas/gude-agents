# v1 release notes: lease-only rate limiting

Rate limiting is a breaking v1 API migration. The engine-facing contract is now `Reserve(ctx, request)`, `Commit(ctx, lease, usage)`, and `Release(ctx, lease)`. Leases are opaque; terminal calls return typed lifecycle errors that support `errors.Is` for duplicate, conflicting, unknown, and expired leases.

Remove direct request admission, usage recording, independent checks, release callbacks, and legacy store implementations. Provide one `agent.RateLimiter` to `agent.WithRateLimiter`; provider attempts, including eligible retries, are then reserved and terminally accounted for by the engine.

RPM and TPM reserve atomically across per-key/global counters. TPM reserves the request estimate plus `MaxTokens` when configured and reconciles confirmed actual usage. Uncertain provider failures settle the reservation conservatively. Memory and Redis retain pending/terminal ledgers long enough to prevent capacity reset and detect invalid terminal actions; pending expiry settles the estimate and returns the typed expired result.

See [rate limiting](rate-limiting.md), the [support matrix](support-matrix.md), and the versioned [evaluation cases](evaluation-cases-v1.md) for operational boundaries and qualification scenarios.

## Removed from the stable surface

These are deliberate breaking scope reductions made before the production v1 release. Each removed feature had no coherent durability or qualification story, and none is replaced by a shim, alias, or fallback. The packages and symbols no longer exist.

### Gemini provider and Gemini RAG embedder

Removed `agent/provider/gemini` and `agent/rag/gemini`, along with their registry entry, model definitions, estimator, and documentation.

Gemini support was removed from the stable framework surface before the
production v1 release. Applications should implement the Provider interface
externally if they need to maintain a private Gemini adapter.

The `Provider` abstraction and the Anthropic, Amazon Bedrock, OpenAI, Ollama, and vLLM adapters are unchanged. Gemini thought-signature support was not added.

### Background tools and automatic re-entry

Removed `tool.NewBackground`, `tool.BackgroundHandler`, `Tool.IsBackground`, `Tool.Ack`, `agent.WithBackgroundNotify`, `agent.ErrAgentShuttingDown`, the dispatch registry, detached handler goroutines, completion notifications, and automatic re-entry model turns.

Run durable asynchronous jobs in an application-owned worker, queue, or
scheduler. A job may invoke an Agent normally, but the framework does not own
cross-process dispatch, ownership, retries, dead-letter handling, or delivery.

`Agent.Shutdown(ctx)` now only flushes a conversation store that implements `agent.Flusher`. It no longer waits for background work because none exists. Same-conversation invocations and `RecoverExecution` calls are still serialized within one `Agent`.

### DynamoDB ConversationStore

Removed `agent/conversation/dynamodb`.

DynamoDB is no longer supplied as a ConversationStore. Migrate canonical
conversation history to a supported append-only store before upgrading.
DynamoDB checkpoint support, where configured, is independent and is not a
conversation-history migration path.

The supported conversation backends are in-memory, SQLite, PostgreSQL, and Redis. `agent/checkpoint/dynamodb` is unchanged.
