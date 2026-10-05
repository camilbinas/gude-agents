# Model Context Protocol

`agent/mcp` discovers MCP tools and converts them to ordinary `tool.Tool` values, so they use the same schema validation, authorization, middleware, events, and audit pipeline.

```go
client, err := mcp.NewStreamableClient(ctx, endpoint,
    mcp.WithHTTPClient(httpClient),
)
if err != nil { return err }
defer client.Close()

tools, err := client.Tools(ctx,
    mcp.WithToolPrefix("docs_"),
    mcp.IncludeTools("search", "fetch"),
)
a, err := agent.New(prov, instructions, agent.WithTools(tools...))
```

Use `NewStdioClient` for subprocess servers, `NewStreamableClient` for current remote HTTP servers, and `NewSSEClient` for older SSE servers. `WithEnv` configures subprocess environment; do not expose secrets in tool descriptions or invocation KV.

`IncludeTools`, `ExcludeTools`, and `WithToolPrefix` control exposed tools. Prefix names when combining servers to avoid collisions.

For concurrent subprocess calls, `NewPool` lazily creates sessions up to `WithPoolSize`; `Pool.Tools` returns tools that lease a session per call. Close clients/pools during application shutdown. The agent's `Shutdown` handles agent background work, not external MCP client lifecycle.

The canonical runnable workflow is [`examples/mcp`](../examples/mcp/), which uses Streamable HTTP. Stdio is the concise alternative:

```go
client, err := mcp.NewStdioClient(ctx, command, args...)
if err != nil { return err }
defer client.Close()
tools, err := client.Tools(ctx)
```

Legacy SSE remains supported for existing servers but is not the primary example path.
