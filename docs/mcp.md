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

Use `NewStdioClient` for subprocess servers and `NewStreamableClient` for remote HTTP servers. The legacy SSE transport is not supported. `WithEnv` configures subprocess environment; do not expose secrets in tool descriptions or invocation KV.

`IncludeTools`, `ExcludeTools`, and `WithToolPrefix` control exposed tools. Prefix names when combining servers to avoid collisions. `IncludeTools` and `ExcludeTools` are mutually exclusive: passing both to the same `Tools()` call returns an error, so choose an allow-list or a deny-list rather than combining them.

A `Client` wraps one session, and that session is safe for concurrent `CallTool` — the SDK multiplexes requests over its JSON-RPC connection — so the returned tools may run in parallel (for example under the agent's parallel tool dispatch) without extra locking. Concurrency is correctness, not throughput: all calls still share one transport (and, for stdio, one subprocess request stream). For higher throughput use a `Pool` (below), which spreads calls across several sessions.

Close clients and pools during application shutdown; the agent does not manage the MCP client lifecycle.

The canonical runnable workflow is [`examples/mcp`](../examples/mcp/), which uses Streamable HTTP. Stdio is the concise alternative:

```go
client, err := mcp.NewStdioClient(ctx, command, args)
if err != nil { return err }
defer client.Close()
tools, err := client.Tools(ctx)
```

## Pooling concurrent stdio calls

A single stdio session serves one request stream. For concurrent tool calls, `NewPool` runs several subprocesses of the same server:

```go
pool, err := mcp.NewPool(ctx, command, args,
    mcp.WithPoolSize(4),           // at most 4 subprocesses (default 5)
    mcp.WithPoolEnv("API_KEY=..."), // environment for every subprocess
)
if err != nil { return err }
defer pool.Close()
tools, err := pool.Tools()
```

The pool starts one session to validate the server and discover tools, then creates more lazily up to the configured size. Each tool call leases a session for its duration, so no session is ever used by two calls at once. When every session is busy, callers wait until one is returned, their context ends, or the pool closes.

- `Close` wakes every waiting caller with an error and closes all idle sessions; a session still leased to a running call is closed when that call finishes.
- A session whose call failed in transport, or whose call was cancelled or timed out (its request may still be running), is closed instead of being reused. The freed slot lets a later call start a replacement while the pool is below its size.
- A JSON-RPC error answered by the server, and a tool result with `IsError`, leave the session healthy and it is reused.
- The pool never becomes a single shared session: the configured size bounds the number of subprocesses.
