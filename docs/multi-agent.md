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
    agent.WithParallelTools(),
)
```

The parent decides when to invoke the child. Child execution uses the current standard context, so cancellation and invocation metadata propagate; its final `Result.Text` becomes the tool result. Configure persistence, retrieval, limits, and observers on each agent according to ownership.

Use parallel tools only when child operations are independent. Shared conversation stores remain protected by CAS revisions; use distinct conversation IDs when agents should not share a transcript.

For process boundaries, use [A2A](a2a.md). For external tool ecosystems rather than autonomous agents, use [MCP](mcp.md).
