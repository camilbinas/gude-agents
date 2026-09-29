# Decision guide

## Invocation style

| Need | Use |
|---|---|
| One final answer and usage | `Agent.Invoke` → `Result` |
| UI/SSE events, tools, widgets, interrupts | `Agent.Stream` → `iter.Seq2[Event, error]` |
| Live answer text only | `Agent.TextStream` → `iter.Seq2[string, error]` |
| Continue a pause | `Agent.Resume` or `Agent.ResumeStream` |
| Typed JSON | `structured.Invoke[T]` |

Consume streams through `EventEnd` when the completed turn must persist. Breaking iteration cancels the run.

## State

| Need | Use |
|---|---|
| Multi-turn transcript | `ConversationStore` with `Context.WithConversationID` |
| User facts across conversations | `memory.Memory[T]` with identity/strict scope |
| Searchable knowledge | `rag.Store` + `rag.Retriever` |
| General versioned snapshots | `checkpoint.Checkpointer` |
| Cross-process paused runs | `InterruptStore` |

Conversation stores protect writes with revisions. Memory and RAG solve different problems and should not be substituted for transcript persistence.

## Tools

Use `WithTools` for a fixed set and `WithToolRegistry` for a dynamic, concurrency-safe set. Prefer typed `tool.New`; use `NewRaw` only when raw JSON is required, `NewRich` for images, and `NewBackground` for durable asynchronous re-entry that survives caller cancellation.

Use tool options for schema, guard, role policy, and approval. Use middleware for cross-cutting execution behavior and observers for telemetry.

## Composition

Use `agent.AgentAsTool` for in-process parent/child delegation, MCP for external tool servers, and A2A for independently deployed agents. See [Multi-agent](multi-agent.md), [MCP](mcp.md), and [A2A](a2a.md).

## Providers and operations

Choose a provider by deployment and model needs; the agent depends only on `Provider.Stream`. Use application `Event` streams for product behavior and operational observers for logs, metrics, traces, and audit. See [Providers](providers.md) and [Observability](observability.md).
