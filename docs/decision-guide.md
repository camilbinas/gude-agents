# Decision Guide — Which Component Should I Use?

This guide answers the most common "which one?" questions by comparing options side-by-side. Each table shows the tradeoffs so you can pick the right component without reading every doc page first.

## Which conversation store?

| | In-Memory | Disk | SQLite | PostgreSQL | Redis | DynamoDB | S3 |
|---|---|---|---|---|---|---|---|
| **Package** | `agent/conversation` | `agent/conversation/disk` | `agent/conversation/sqlite` | `agent/conversation/postgres` | `agent/conversation/redis` | `agent/conversation/dynamodb` | `agent/conversation/s3` |
| **Persistence** | Process lifetime | File per conversation | Single file | PostgreSQL server | Redis server | DynamoDB table | S3 bucket |
| **External dependency** | None | None | None | PostgreSQL | Redis | AWS DynamoDB | AWS S3 |
| **TTL / auto-expiry** | — | — | — | — | ✓ | ✓ | Via lifecycle rules |
| **ACID** | — | Atomic rename | ✓ WAL | ✓ full | — | ✓ single-item | — |
| **Size limit** | Process memory | Filesystem | ~281 TB | 1 GB / field | `maxmemory` | 400 KB / item | 50 TB / object |
| **Use when** | Tests, short-lived | CLI tools, dev | Local apps, single-node | Production, multi-node | Multi-process, caching | Serverless, AWS-native | Archival, AWS-native |

DynamoDB's 400 KB item limit can bite on long-running conversations — pair it with `conversation.NewWindow` or `conversation.NewSummary` to keep items small.

See [Conversation System](conversation.md) for full options and code examples.

## Which vector store?

| | `rag.MemoryStore` | Redis Stack | PostgreSQL + pgvector |
|---|---|---|---|
| **Package** | `agent/rag` | `agent/rag/redis` | `agent/rag/postgres` |
| **Persistence** | No — process lifetime | Yes | Yes |
| **External dependency** | None | Redis Stack (not community Redis) | PostgreSQL + pgvector extension |
| **ANN search** | Brute-force cosine | RediSearch HNSW | pgvector IVFFlat / HNSW |
| **`VectorStoreManager`** | ✓ | ✓ | ✓ |
| **`DeleteByMetadata`** | ✓ | FT.SEARCH then DEL | JSONB containment (`@>`) |
| **Use when** | Prototyping, tests | High-throughput, Redis already in stack | Production with Postgres already in stack |

All three implement `VectorStoreManager` and work identically with `rag.NewRetriever`, `rag.Ingest`, and `NewRetrieverTool`.

See [RAG Pipeline](rag.md) and [Redis Providers](redis.md) for options and code examples.

## Which memory backend?

Long-term memory stores typed facts scoped to an identifier (user, project, tenant) with semantic recall.

| | In-Memory | Redis | PostgreSQL |
|---|---|---|---|
| **Package** | `agent/memory` | `agent/memory/redis` | `agent/memory/postgres` |
| **Persistence** | No | Yes | Yes |
| **External dependency** | None | Redis Stack (RediSearch) | PostgreSQL + pgvector |
| **Index creation** | Automatic | Automatic | Caller creates the table |
| **Filters** | `RecallOption` | TAG / NUMERIC fields | Full SQL (`WithRawFilter`) |
| **`ForgetAll`** | ✓ | ✓ | ✓ |
| **Use when** | Development, tests | Redis already in stack | Postgres already in stack, complex filters needed |

All three implement `Memory[T]`, `Updater[T]`, `Forgetter[T]`, and `BulkForgetter[T]`. The `NewRememberTool`, `NewRecallTool`, `NewUpdateTool`, and `NewForgetTool` constructors work with any backend.

See [Long-Term Memory](memory.md) for struct tags, recall filters, and multi-scope patterns.

## `WithRetriever` vs `NewRetrieverTool`

Both wire a retriever into an agent. The difference is who decides when retrieval happens.

| | `WithRetriever` | `NewRetrieverTool` |
|---|---|---|
| **When retrieves** | Every invocation, before the first LLM call | LLM decides when to call it |
| **LLM calls** | 1 | 2+ (one to decide to search, one to answer) |
| **Speed** | Faster — retrieval and LLM call don't chain | Slower — extra round-trip |
| **Token cost** | Always pays retrieval cost | Only when LLM calls the tool |
| **Best for** | Document Q&A where context is always needed | Agents with multiple tools where search is optional |

Use `WithRetriever` when every question needs document context (e.g. a PDF assistant). Use `NewRetrieverTool` when the agent has other tools and retrieval is just one option.

See [RAG Pipeline](rag.md) for full code examples.

## `AgentAsTool` vs `a2a.NewClient`

Both let an orchestrator call another agent as a tool. The choice comes down to deployment topology.

| | `AgentAsTool` | `a2a.NewClient` |
|---|---|---|
| **Transport** | In-process Go function call | HTTP (A2A JSON-RPC) |
| **Deployment** | Same binary, same process | Remote service, separate binary |
| **Discovery** | Manual — you write the tool name and description | Automatic — fetched from `/.well-known/agent.json` |
| **Protocol** | gude-agents internal | Standard A2A — works with any compliant agent |
| **Latency** | Microseconds | Network round-trip |
| **Setup** | `agent.AgentAsTool(name, desc, child)` | `a2a.NewClient(ctx, baseURL)` |
| **Use when** | All agents are in the same Go binary | Agents run as separate services, or the remote is not Go |

Use `AgentAsTool` when you control all agents and they run in the same process. Use `a2a.NewClient` when agents are deployed as independent services, when interoperability with non-Go agents matters, or when you want automatic skill discovery.

See [Multi-Agent Composition](multi-agent.md) and [A2A Protocol](a2a.md) for full code examples.

## Handoffs vs Tool Approval

Both pause the agent loop and hand control to a human. The difference is what gets paused and what the human provides.

| | Handoffs | Tool Approval |
|---|---|---|
| **Triggered by** | LLM calling `NewHandoffTool` | LLM calling any `RequiresApproval` tool |
| **What pauses** | The whole agent loop | The loop, before one specific tool runs |
| **Human provides** | Free-form text response | `tool.Decision{Allow: bool, Reason: string}` |
| **Resume method** | `Agent.ResumeInvoke` / `Agent.Resume` | `Agent.ResumeWithApprovalInvoke` / `Agent.ResumeWithApproval` |
| **On denial** | N/A — human always provides an answer | Structured denial injected as tool result; loop continues |
| **Async friendly** | Yes | Yes |
| **Use when** | Agent needs a human-provided answer to continue | You want a gate before a destructive/sensitive tool runs |

Use handoffs when the agent needs information or a decision it can't determine on its own. Use tool approval when you want an explicit allow/deny gate before a specific tool handler executes, regardless of what the LLM intended.

See [Handoffs](handoff.md) and [Tool Approval](tool-approval.md) for full code examples.

## `Invoke` vs `InvokeStream` vs `InvokeEventStream`

All three run the same agent loop. The difference is how results are delivered to the caller.

| | `Invoke` | `InvokeStream` | `InvokeEventStream` |
|---|---|---|---|
| **Return type** | `(string, error)` | `error` (chunks via callback) | `<-chan AgentEvent` |
| **Output delivery** | Entire response at once | Text chunks as they arrive | Typed events for every observable step |
| **Streaming to user** | No | Yes — via `StreamCallback` | Yes — via event channel |
| **Tool call visibility** | No | No | Yes — `EventToolCallStart`, `EventToolCallEnd` |
| **Thinking chunk visibility** | No | No | Yes — `EventThinkingChunk` |
| **Handoff / approval events** | No | No | Yes — `EventHandoffRequested`, `EventToolApprovalRequired` |
| **Custom events** | No | No | Yes — `EventCustom` via `c.EmitEvent` |
| **Token usage** | `c.Usage()` after call | `c.Usage()` after call | `EventInvokeEnd.Usage` |
| **Concurrent calls on same context** | Safe | Safe | Safe — context is cloned internally |
| **Use when** | Simple single-shot responses | Streaming text to the user | Building UIs, SSE feeds, CLI dashboards, or when you need tool/event visibility |

`Invoke` is a convenience wrapper over `InvokeStream`. `InvokeEventStream` runs a separate internal context clone so token usage is not written back to the caller's context — read it from the terminal `EventInvokeEnd` event.

See [Agent API](agent-api.md) for the full event taxonomy and `InvokeEventStream` options.
