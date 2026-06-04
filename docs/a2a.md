# Agent-to-Agent (A2A) Protocol

The `a2a` package lets gude-agents speak the [Google A2A protocol](https://github.com/google-a2a/A2A). You can connect to any A2A-compliant remote agent and call its skills as regular `tool.Tool` values, or expose your own agent so other agents can call it the same way.

The package covers three scenarios:

- **Client** — discover a remote agent's skills and wire them into a local orchestrator
- **Server** — expose a local agent as an A2A server that any compliant client can reach
- **MultiServer** — host multiple agents on one HTTP server with path-prefix routing

## Installation

```bash
go get github.com/camilbinas/gude-agents/agent/a2a
```

## Using a Remote Agent as Tools

### NewClient

```go
func NewClient(ctx context.Context, baseURL string, opts ...ClientOption) (*Client, error)
```

Connects to a remote A2A agent. On construction it fetches `{baseURL}/.well-known/agent.json`, parses the Agent Card, and converts each skill into a `tool.Tool`. Returns an error if the card cannot be fetched or parsed.

```go
client, err := a2a.NewClient(ctx, "http://localhost:8080")
if err != nil {
    log.Fatal(err)
}
defer client.Close()
```

#### Client Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithClientHTTPClient(hc *http.Client)` | 30 s timeout | Custom HTTP client for card discovery and task execution |

### client.Tools

```go
func (c *Client) Tools(ctx context.Context, opts ...ToolsOption) ([]tool.Tool, error)
```

Returns `tool.Tool` values for the remote agent's skills. Each tool sends a `SendMessage` JSON-RPC request to the remote agent when invoked. Without options, all skills are returned.

```go
// All skills
tools, err := client.Tools(ctx)

// Only the named skills
tools, err := client.Tools(ctx, a2a.IncludeSkills("get_weather", "get_forecast"))

// Everything except the named skills
tools, err := client.Tools(ctx, a2a.ExcludeSkills("debug_internal"))
```

#### ToolsOption

| Option | Description |
|--------|-------------|
| `IncludeSkills(ids ...string)` | Restrict to only the named skill IDs |
| `ExcludeSkills(ids ...string)` | Filter out the named skill IDs |

`IncludeSkills` takes precedence over `ExcludeSkills` if both are provided.

### client.Card

```go
func (c *Client) Card() *a2a.AgentCard
```

Returns the Agent Card fetched during construction. Use it to inspect the remote agent's name, skills, and capabilities before deciding which skills to expose.

```go
card := client.Card()
fmt.Printf("Remote agent: %s (%d skills)\n", card.Name, len(card.Skills))
```

### client.Close

```go
func (c *Client) Close() error
```

Closes idle HTTP connections. Safe to call multiple times. Call via `defer` after construction.

## Hosting a Local Agent as an A2A Server

### NewExecutor

```go
func NewExecutor(a *agent.Agent, logger *slog.Logger) *Executor
```

Wraps an `*agent.Agent` to implement the `a2asrv.AgentExecutor` interface. The executor translates incoming A2A messages into `agent.InvokeStream` calls and emits artifact events for each response chunk. If `logger` is nil, `slog.Default()` is used.

`NewExecutor` is a low-level building block. In most cases you want `NewServer`, which creates the executor, derives the Agent Card, and wires up the HTTP handler for you.

### NewServer

```go
func NewServer(a *agent.Agent, cardOpts []CardOption, serverOpts ...ServerOption) (*Server, error)
```

Creates a ready-to-serve A2A server. It derives the Agent Card from the agent's metadata, creates an `Executor`, and attaches both JSON-RPC and REST HTTP handlers. Returns an error if `a` is nil.

```go
srv, err := a2a.NewServer(a,
    []a2a.CardOption{
        a2a.WithCardURL("http://localhost:8080"),
        a2a.WithCardVersion("1.0.0"),
        a2a.WithCardDescription("A travel assistant that provides weather info"),
    },
)
if err != nil {
    log.Fatal(err)
}

ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

if err := srv.ListenAndServe(ctx, ":8080"); err != nil {
    log.Fatal(err)
}
```

#### Server Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithLogger(l *slog.Logger)` | `slog.Default()` | Logger for request and lifecycle events |
| `WithGracefulTimeout(d time.Duration)` | `30s` | How long to wait for in-flight requests during shutdown |
| `WithHandlerOptions(opts ...a2asrv.RequestHandlerOption)` | — | Pass additional options to the underlying SDK request handler |

### Agent Card Derivation

`DeriveCard` is called internally by `NewServer` and `NewMultiServer` to produce the Agent Card from the agent's exported metadata:

- **Name** — `agent.Name()`
- **Description** — `agent.Instructions()` truncated to 200 characters
- **Skills** — one skill per tool in `agent.ToolSpecs()`, using the tool's name and description
- **Capabilities** — streaming enabled by default

Override any field with `CardOption` values:

| Option | Description |
|--------|-------------|
| `WithCardDescription(desc string)` | Override the auto-derived description |
| `WithCardVersion(version string)` | Set the version field (default `"1.0.0"`) |
| `WithCardURL(url string)` | Set the endpoint URL and register a JSON-RPC interface |
| `WithCardSkills(skills []a2a.AgentSkill)` | Replace the auto-derived skills entirely |
| `WithCardCapabilities(caps a2a.AgentCapabilities)` | Override the capabilities block |

### Server Handlers

```go
func (s *Server) Handler() http.Handler
func (s *Server) RESTHandler() http.Handler
```

`Handler()` serves the A2A JSON-RPC transport. `RESTHandler()` serves the A2A REST transport. Both mount `/.well-known/agent.json` for card discovery. Use these when you want to embed the A2A server inside a larger `http.ServeMux` rather than call `ListenAndServe` directly.

```go
mux := http.NewServeMux()
mux.Handle("/a2a/", http.StripPrefix("/a2a", srv.Handler()))
mux.Handle("/health", healthHandler)
http.ListenAndServe(":8080", mux)
```

## Multi-Agent Server

`MultiServer` hosts multiple agents on one HTTP server, each at its own path prefix. Each agent gets an independent Agent Card and request handler.

### NewMultiServer

```go
func NewMultiServer(registrations []AgentRegistration, opts ...MultiServerOption) (*MultiServer, error)
```

Creates the multi-server from a list of `AgentRegistration` values. Returns an error if any prefix is duplicated or if any agent is nil.

```go
ms, err := a2a.NewMultiServer([]a2a.AgentRegistration{
    {Prefix: "/agents/summarizer", Agent: summarizer},
    {Prefix: "/agents/translator", Agent: translator, CardOpts: []a2a.CardOption{
        a2a.WithCardVersion("2.0.0"),
    }},
})
```

#### AgentRegistration

| Field | Type | Description |
|-------|------|-------------|
| `Prefix` | `string` | URL path prefix for this agent (e.g. `"/agents/summarizer"`) |
| `Agent` | `*agent.Agent` | The agent to host at this prefix |
| `CardOpts` | `[]CardOption` | Optional card overrides (in addition to `WithCardURL(Prefix)` which is always applied) |

#### MultiServer Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithMultiServerLogger(l *slog.Logger)` | `slog.Default()` | Logger for the multi-server |
| `WithMultiServerGracefulTimeout(d time.Duration)` | `30s` | Graceful shutdown timeout |

### MultiServer Handlers

```go
func (ms *MultiServer) Handler() http.Handler
func (ms *MultiServer) RESTHandler() http.Handler
```

`Handler()` routes JSON-RPC requests by path prefix. `RESTHandler()` routes REST requests by path prefix. For each registered prefix, both handlers serve:

- `{prefix}/.well-known/agent-card.json` — Agent Card for that agent
- `{prefix}/` — request handler for that agent

Unmatched paths return 404.

### ListenAndServe

```go
func (ms *MultiServer) ListenAndServe(ctx context.Context, addr string) error
```

Starts an HTTP server with the JSON-RPC handler and performs a graceful shutdown when `ctx` is canceled.

## A2A vs AgentAsTool

Both patterns let an orchestrator agent call another agent as a tool. Choose based on deployment topology:

| | `AgentAsTool` | `a2a.NewClient` |
|---|---|---|
| Transport | In-process Go function call | HTTP (A2A JSON-RPC) |
| Deployment | Same binary, same process | Remote service, separate binary |
| Discovery | Manual — you write the tool name and description | Automatic — fetched from `/.well-known/agent.json` |
| Protocol | Proprietary (gude-agents internal) | Standard A2A — works with any compliant agent |
| Latency | Microseconds | Network round-trip |
| Setup | `agent.AgentAsTool(name, desc, child)` | `a2a.NewClient(ctx, baseURL)` |
| Use when | All agents are in the same Go binary | Agents run as separate services, or the remote agent is not Go |

Use `AgentAsTool` when you control all agents and they run in the same process. Use `a2a.NewClient` when agents are deployed as independent services, when interoperability with non-Go agents matters, or when you want automatic skill discovery.

## Code Example

This example wires together all three layers: a server agent, a client that discovers its skills, and an orchestrator that uses those skills as tools.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os/signal"
    "syscall"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/a2a"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    provider := bedrock.Must(bedrock.Standard())

    // --- Server side: expose a summarizer agent over A2A ---
    summarizer, err := agent.Default(
        provider,
        prompt.Text("You are a summarization expert. Produce concise summaries."),
        nil,
        agent.WithName("summarizer"),
    )
    if err != nil {
        log.Fatal(err)
    }

    ms, err := a2a.NewMultiServer([]a2a.AgentRegistration{
        {Prefix: "/agents/summarizer", Agent: summarizer},
    })
    if err != nil {
        log.Fatal(err)
    }

    // Start the server in the background.
    go func() {
        if err := ms.ListenAndServe(ctx, ":8080"); err != nil {
            log.Printf("server stopped: %v", err)
        }
    }()

    // --- Client side: discover the remote agent and use its skills ---
    client, err := a2a.NewClient(ctx, "http://localhost:8080/agents/summarizer")
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    fmt.Printf("Discovered: %s (%d skills)\n", client.Card().Name, len(client.Card().Skills))

    remoteTools, err := client.Tools(ctx)
    if err != nil {
        log.Fatal(err)
    }

    // --- Orchestrator: wire remote tools into a local agent ---
    orchestrator, err := agent.Default(
        provider,
        prompt.Text("You are an orchestrator. Use the available tools to answer questions."),
        remoteTools,
        agent.WithName("orchestrator"),
    )
    if err != nil {
        log.Fatal(err)
    }

    result, err := orchestrator.Invoke(agent.NewContext(ctx),
        "Summarize: Go is a statically typed, compiled language designed at Google.")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Result: %s\n", result)
}
```

See also `examples/a2a-client`, `examples/a2a-server`, and `examples/a2a-multiserver` for standalone runnable versions.

## See Also

- [Multi-Agent Composition](multi-agent.md) — `AgentAsTool` and orchestrator patterns for in-process agents
- [Tool System](tools.md) — how `tool.Tool` works; remote skills are indistinguishable from local tools
- [MCP](mcp.md) — similar client pattern for the Model Context Protocol
- [AgentCore](agentcore.md) — `WithA2A` / `WithA2AAddr` options to configure A2A on AgentCore runtimes
- [Getting Started](getting-started.md) — installation and first agent
