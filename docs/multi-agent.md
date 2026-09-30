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

The parent decides when to invoke the child. Child execution uses the current standard context, so cancellation and invocation metadata propagate; its final `Result.Text` becomes the tool result. Configure persistence, retrieval, limits, and observers on each agent according to ownership.

Tool calls run in parallel by default, so independent specialist calls execute simultaneously. Use `agent.WithSequentialTools()` when child operations are not independent and must run one at a time. Shared conversation stores remain protected by CAS revisions; use distinct conversation IDs when agents should not share a transcript.

For process boundaries, use [A2A](a2a.md). For external tool ecosystems rather than autonomous agents, use [MCP](mcp.md).
