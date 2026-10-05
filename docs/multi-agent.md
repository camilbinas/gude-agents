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

Child execution receives a clone of the parent invocation context: cancellation, identity, scopes, principal, and configuration propagate, while the child gets an isolated invocation KV store. Its final `Result.Text` becomes the parent tool result. Configure persistence, retrieval, limits, and observers on each agent according to ownership.

When a child has a `ConversationStore`, `AgentAsTool` automatically isolates it from the parent transcript. The child conversation ID is deterministically derived from the parent conversation ID, tool-call ID, wrapper tool name, and child agent identity. Normal callers should not manually invent a separate child ID. A persistent child therefore requires the parent invocation to have a conversation ID and to be inside a tool call.

Child interrupts do not propagate through `AgentAsTool`. If a child pauses for approval or human input, the wrapper consumes the child interrupt and returns a tool error to the parent; durable child continuation is not yet supported through this adapter.

Tool calls run in parallel by default, so independent specialist calls execute simultaneously. Use `agent.WithSequentialTools()` when child operations are not independent and must run one at a time.

For process boundaries, use [A2A](a2a.md). For external tool ecosystems rather than autonomous agents, use [MCP](mcp.md).
