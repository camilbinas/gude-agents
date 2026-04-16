# MCP (Model Context Protocol)

The `mcp` package connects to external [MCP servers](https://modelcontextprotocol.io/) and exposes their tools as regular `tool.Tool` values. MCP tools are indistinguishable from local tools — they work with all agent features including middleware, guardrails, parallel execution, and multi-agent orchestration.

This lets you use any MCP-compatible server (filesystem access, databases, web search, code execution, etc.) as tools in your agent without writing custom tool handlers.

## Client

`Client` manages the connection to an MCP server and provides tool discovery. Three constructors are available depending on how the server is hosted.

### NewStdioClient

```go
func NewStdioClient(ctx context.Context, command string, args []string, opts ...Option) (*Client, error)
```

Connects to an MCP server via stdin/stdout of a subprocess. The `command` and `args` specify the server binary to run. The client starts the subprocess, performs the MCP handshake, and is ready to discover tools.

Returns an error if the subprocess fails to start or the MCP initialization handshake fails.

| Parameter | Type | Description |
|---|---|---|
| `ctx` | `context.Context` | Context for the connection and handshake |
| `command` | `string` | The server command to run (e.g., `"npx"`, `"uvx"`, `"python"`) |
| `args` | `[]string` | Arguments for the command |
| `opts` | `...Option` | Optional configuration (e.g., environment variables) |

### NewStreamableClient

```go
func NewStreamableClient(ctx context.Context, endpoint string, opts ...Option) (*Client, error)
```

Connects to a remote MCP server using the Streamable HTTP transport — the current MCP standard as of the 2025-03-26 spec. Use this for any modern remote MCP server.

```go
client, err := mcp.NewStreamableClient(ctx, "https://my-mcp-server.example.com/mcp")
```

### NewSSEClient

```go
func NewSSEClient(ctx context.Context, endpoint string, opts ...Option) (*Client, error)
```

Connects to a remote MCP server using the legacy SSE transport (HTTP GET for server-sent events). Use this for servers that implement the pre-2025-03-26 MCP spec. For newer servers, prefer `NewStreamableClient`.

```go
client, err := mcp.NewSSEClient(ctx, "https://legacy-mcp-server.example.com/sse")
```

### Options

#### WithEnv

```go
func WithEnv(env ...string) Option
```

Sets environment variables for the MCP server subprocess. Each entry should be in `"KEY=VALUE"` format. These are appended to the current process environment. Use this to pass API keys or configuration to the server.

```go
mcpClient, err := mcp.NewStdioClient(ctx,
    "npx", []string{"-y", "@modelcontextprotocol/server-github"},
    mcp.WithEnv("GITHUB_TOKEN=ghp_xxxxxxxxxxxx"),
)
```

#### WithHTTPClient

```go
func WithHTTPClient(hc *http.Client) Option
```

Sets a custom HTTP client for SSE and Streamable HTTP connections. Use this to configure timeouts, TLS certificates, authentication headers, or proxies.

```go
httpClient := &http.Client{
    Timeout: 30 * time.Second,
    Transport: &http.Transport{
        TLSClientConfig: &tls.Config{...},
    },
}

client, err := mcp.NewStreamableClient(ctx,
    "https://my-mcp-server.example.com/mcp",
    mcp.WithHTTPClient(httpClient),
)
```

### Tools

```go
func (c *Client) Tools(ctx context.Context, opts ...ToolsOption) ([]tool.Tool, error)
```

Discovers all tools from the MCP server and returns them as `tool.Tool` values. Handles pagination automatically if the server returns multiple pages. Each MCP tool is converted to a `tool.Tool` with:

- `Spec.Name` — the MCP tool name
- `Spec.Description` — the MCP tool description
- `Spec.InputSchema` — the MCP tool's JSON Schema, converted to `map[string]any`
- `Handler` — calls `session.CallTool` on the MCP server, marshalling input and extracting text from the response

Use `WithInclude` or `WithExclude` to filter which tools are returned.

#### WithInclude

```go
func WithInclude(names ...string) ToolsOption
```

Restricts `Tools()` to only the named tools. All other tools from the server are ignored.

```go
// Only expose read operations to the agent
tools, err := client.Tools(ctx, mcp.WithInclude("read_file", "list_directory"))
```

#### WithExclude

```go
func WithExclude(names ...string) ToolsOption
```

Filters out the named tools from the result. All other tools are returned.

```go
// Expose everything except destructive operations
tools, err := client.Tools(ctx, mcp.WithExclude("delete_file", "move_file"))
```

> `WithInclude` takes precedence over `WithExclude` if both are provided.

### Close

```go
func (c *Client) Close() error
```

Disconnects from the MCP server and terminates the subprocess. Call this when you're done with the client (typically via `defer`).

## How It Works

1. `NewStdioClient` starts the MCP server as a subprocess and communicates via stdin/stdout using the MCP JSON-RPC protocol. `NewStreamableClient` and `NewSSEClient` connect to a remote HTTP endpoint instead.
2. `Tools()` calls `tools/list` on the server, iterating through all pages, and converts each MCP tool definition into a `tool.Tool`.
3. When the agent calls an MCP tool, the handler sends a `tools/call` request to the server with the LLM's input, waits for the response, and extracts text content from the result.
4. The agent sees MCP tools exactly like local tools — the same `tool.Spec` and handler interface.

## Code Example

This example connects to the official MCP "everything" test server, discovers its tools, and uses them through an agent:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/mcp"
)

func main() {
    ctx := context.Background()

    // Connect to an MCP server via stdio.
    mcpClient, err := mcp.NewStdioClient(ctx,
        "npx", []string{"-y", "@modelcontextprotocol/server-everything"},
    )
    if err != nil {
        log.Fatal(err)
    }
    defer mcpClient.Close()

    // Discover tools — these are regular tool.Tool values.
    mcpTools, err := mcpClient.Tools(ctx)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Discovered %d MCP tools\n", len(mcpTools))

    // Create an agent with MCP tools.
    provider, err := bedrock.ClaudeSonnet4_6()
    if err != nil {
        log.Fatal(err)
    }

    a, err := agent.Default(
        provider,
        prompt.Text("You are a helpful assistant. Use the available tools when needed."),
        mcpTools,
    )
    if err != nil {
        log.Fatal(err)
    }

    result, _, err := a.Invoke(ctx, "Use the echo tool to say hello")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result)
}
```

## Mixing MCP and Local Tools

MCP tools and local tools are both `tool.Tool` values, so you can freely combine them:

```go
// Local tool
weatherTool := tool.New("get_weather",
    "Get weather for a city.",
    func(ctx context.Context, in WeatherInput) (string, error) {
        return fmt.Sprintf("Weather in %s: 22°C", in.City), nil
    },
)

// MCP tools from a server
mcpTools, err := mcpClient.Tools(ctx)
if err != nil {
    log.Fatal(err)
}

// Combine them
allTools := append([]tool.Tool{weatherTool}, mcpTools...)

a, err := agent.Default(provider, instructions, allTools)
```

## Multiple MCP Servers

Connect to multiple MCP servers and merge their tools:

```go
// Filesystem server
fsClient, err := mcp.NewStdioClient(ctx,
    "npx", []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
)
if err != nil {
    log.Fatal(err)
}
defer fsClient.Close()

// GitHub server
ghClient, err := mcp.NewStdioClient(ctx,
    "npx", []string{"-y", "@modelcontextprotocol/server-github"},
    mcp.WithEnv("GITHUB_TOKEN=ghp_xxxxxxxxxxxx"),
)
if err != nil {
    log.Fatal(err)
}
defer ghClient.Close()

// Discover and merge tools
fsTools, _ := fsClient.Tools(ctx)
ghTools, _ := ghClient.Tools(ctx)

allTools := append(fsTools, ghTools...)

a, err := agent.Default(provider, instructions, allTools)
```

## Using MCP Tools with Multi-Agent

MCP tools work with all agent patterns, including multi-agent orchestration:

```go
// Child agent with MCP filesystem tools
fsAgent, err := agent.Worker(haiku, fsInstructions, fsTools)
if err != nil {
    log.Fatal(err)
}

// Child agent with MCP GitHub tools
ghAgent, err := agent.Worker(haiku, ghInstructions, ghTools)
if err != nil {
    log.Fatal(err)
}

// Orchestrator delegates to specialists
orchestrator, err := agent.Orchestrator(sonnet, instructions, []tool.Tool{
    agent.AgentAsTool("ask_filesystem", "File operations", fsAgent),
    agent.AgentAsTool("ask_github", "GitHub operations", ghAgent),
})
```

## Common MCP Servers

Here are some popular MCP servers you can use:

| Server | Command | Description |
|--------|---------|-------------|
| Everything (test) | `npx -y @modelcontextprotocol/server-everything` | Test server with echo, add, and other demo tools |
| Filesystem | `npx -y @modelcontextprotocol/server-filesystem /path` | Read/write files, list directories |
| GitHub | `npx -y @modelcontextprotocol/server-github` | GitHub API (needs `GITHUB_TOKEN`) |
| PostgreSQL | `npx -y @modelcontextprotocol/server-postgres` | Query PostgreSQL databases |
| Brave Search | `npx -y @modelcontextprotocol/server-brave-search` | Web search (needs `BRAVE_API_KEY`) |

See [MCP Servers](https://github.com/modelcontextprotocol/servers) for a full list.

## Connection Pool (High Concurrency)

`Client` uses a single connection to a single server process. For production workloads with many concurrent users, use `Pool` instead. It manages multiple MCP server subprocesses and distributes tool calls across them.

### NewPool

```go
func NewPool(ctx context.Context, command string, args []string, opts ...PoolOption) (*Pool, error)
```

Creates a connection pool. Starts one initial connection to discover tools and validate the server, then lazily creates additional connections up to `maxSize` as demand requires.

| Parameter | Type | Description |
|---|---|---|
| `ctx` | `context.Context` | Context for the initial connection |
| `command` | `string` | The server command to run |
| `args` | `[]string` | Arguments for the command |
| `opts` | `...PoolOption` | Pool configuration |

### Pool Options

#### WithPoolSize

```go
func WithPoolSize(n int) PoolOption
```

Sets the maximum number of concurrent MCP server connections. Defaults to 5. Each connection is a separate server subprocess.

#### WithPoolEnv

```go
func WithPoolEnv(env ...string) PoolOption
```

Sets environment variables for all MCP server subprocesses.

### Pool.Tools

```go
func (p *Pool) Tools() ([]tool.Tool, error)
```

Returns `tool.Tool` values that use the pool for every call. Tool definitions are cached from the initial connection — this method does not make additional network calls. Each tool call acquires a connection, executes the MCP call, and returns the connection to the pool.

### Pool.Close

```go
func (p *Pool) Close() error
```

Shuts down all connections and terminates all subprocesses.

### Pool.Size

```go
func (p *Pool) Size() int
```

Returns the current number of active connections in the pool.

### How the Pool Works

1. On creation, one subprocess is started to discover tools and validate the server.
2. When a tool is called, the handler tries to grab an idle connection from a buffered channel.
3. If no idle connection is available and the pool hasn't reached `maxSize`, a new subprocess is started.
4. If the pool is at capacity, the caller blocks until a connection is returned.
5. After the tool call completes, the connection is returned to the idle channel.

This means connections are created lazily — if you set `WithPoolSize(10)` but only have 3 concurrent tool calls, only 3 subprocesses will be running.

### Pool Code Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "sync"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/memory"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/mcp"
    "github.com/camilbinas/gude-agents/agent/memory/redis"
)

func main() {
    ctx := context.Background()

    // Create a pool with up to 10 concurrent MCP server connections.
    pool, err := mcp.NewPool(ctx,
        "npx", []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
        mcp.WithPoolSize(10),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer pool.Close()

    // Get pooled tools — safe for concurrent use.
    mcpTools, err := pool.Tools()
    if err != nil {
        log.Fatal(err)
    }

    provider, err := bedrock.ClaudeSonnet4_6()
    if err != nil {
        log.Fatal(err)
    }

    // Simulate 50 concurrent users, each with their own conversation.
    var wg sync.WaitGroup
    for i := range 50 {
        wg.Add(1)
        go func(userID int) {
            defer wg.Done()

            conversationID := fmt.Sprintf("user-%d", userID)

            // Each user gets their own agent with their own memory,
            // but they all share the same pooled MCP tools.
            store := memory.NewStore()
            a, err := agent.Default(
                provider,
                prompt.Text("You are a helpful assistant with filesystem access."),
                mcpTools,
                agent.WithMemory(store, conversationID),
            )
            if err != nil {
                log.Printf("user %d: agent error: %v", userID, err)
                return
            }

            result, _, err := a.Invoke(ctx, "List the files in /tmp")
            if err != nil {
                log.Printf("user %d: invoke error: %v", userID, err)
                return
            }
            fmt.Printf("User %d: %s\n", userID, result[:min(len(result), 80)])
        }(i)
    }
    wg.Wait()
    fmt.Printf("Pool used %d connections\n", pool.Size())
}
```

### Client vs Pool

| | `Client` | `Pool` |
|---|---|---|
| Connections | 1 subprocess | Up to N subprocesses |
| Concurrency | Sequential tool calls | Concurrent tool calls across connections |
| Use case | CLI tools, single-user apps, development | Web servers, multi-user apps, production |
| Creation | `NewStdioClient` | `NewPool` |
| Tool discovery | `client.Tools(ctx)` | `pool.Tools()` (cached, no network call) |

## See Also

- [Tool System](tools.md) — how tools work in the agent loop
- [Multi-Agent Composition](multi-agent.md) — using MCP tools with orchestrator patterns
- [Middleware](middleware.md) — wrapping MCP tool execution with logging, metrics, etc.
- [Guardrails](guardrails.md) — validating input/output around MCP tool calls
- [Getting Started](getting-started.md) — installation and first agent
