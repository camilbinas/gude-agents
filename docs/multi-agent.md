# Multi-agent composition

Wrap a configured child agent as a normal tool:

```go
researcher, err := agent.New(researchProvider, "Research facts and cite sources.",
    agent.WithTools(search),
)
researchTool := agent.AgentAsTool(
    "research",
    "Delegate a research question",
    researcher,
)
coordinator, err := agent.New(mainProvider, "Coordinate specialist work.",
    agent.WithTools(researchTool),
)
```

Child execution receives a clone of the parent invocation context: cancellation, identity, scopes, principal, and configuration propagate, while the child gets an isolated invocation KV store. Its final `Result.Text` becomes the parent tool result. Configure persistence, retrieval, limits, and hooks on each agent according to ownership.

When a child has a `ConversationStore`, `AgentAsTool` automatically isolates it from the parent transcript. The child conversation ID is deterministically derived from the parent conversation ID, tool-call ID, wrapper tool name, and child agent identity. Normal callers should not manually invent a separate child ID. A persistent child therefore requires the parent invocation to have a conversation ID and to be inside a tool call.

A child follows the same rule as any agent: pausing needs both a conversation store and an execution store, and a child without them fails with `agent.ErrPauseStorageRequired`, which the parent sees as a tool error.

When a child with both stores pauses, and the parent has both stores too, the parent pauses. The parent `Result` carries one `agent.InterruptNested` interrupt whose `Nested.Child` is the child's own approval or question; resume the parent with the normal `Approve`, `Deny`, `Decide`, `Respond`, or `RespondWith`, and the child continues from its pause with its answer as the tool result. This works across multiple levels and is durable across restart and `Snapshot`/`Restore`. See [Nested pauses through AgentAsTool](interrupts.md#nested-pauses-through-agentastool).

If the parent has no pause stores, the child pause is canceled and returned as a tool error.

Tool calls run in parallel by default, so independent specialist calls execute simultaneously. Use `agent.WithToolExecution(agent.ToolExecutionSequential)` when child operations are not independent and must run one at a time, or `agent.WithMaxConcurrentTools(n)` to bound how many children run at once.

For external tool ecosystems rather than in-process agents, use [MCP](mcp.md). Gude does not ship a cross-process agent protocol; expose a remote agent behind your own service API and wrap it as a tool.
