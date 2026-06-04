# AWS Bedrock AgentCore

The `agentcore` package integrates gude-agents with [AWS Bedrock AgentCore](https://docs.aws.amazon.com/bedrock/latest/userguide/agentcore.html). It ships three integration surfaces:

- **`NewServer`** — an HTTP server that implements the AgentCore Runtime contract (`POST /invocations`, `GET /ping`), suitable for containerized deployments.
- **`NewRuntime`** — a long-running worker adapter that registers with AgentCore, polls for events, and submits responses back. Use this for the AgentCore worker protocol.
- **`NewConversation`** — an `agent.Conversation` backed by AgentCore's Memory service, so conversation history survives across sessions without a separate database.
- **Built-in tools** — `Browser` and `CodeInterpreter` wrap AgentCore's managed browser and code interpreter services.

The package lives in its own Go module to keep the AgentCore SDK out of the core agent module's dependency tree.

## Installation

```bash
go get github.com/camilbinas/gude-agents/agent/agentcore
```

## Server

`NewServer` wraps an `*agent.Agent` in an HTTP server that satisfies the AgentCore Runtime container contract. On every `POST /invocations`, it reads the request body, sets the conversation ID from the `X-Amzn-Bedrock-AgentCore-Runtime-Session-Id` header, invokes the agent, and writes the response. `GET /ping` reports the server's health status.

```go
srv, err := agentcore.NewServer(myAgent,
    agentcore.WithAddr(":8080"),
)
if err != nil {
    log.Fatal(err)
}
if err := srv.ListenAndServe(ctx); err != nil {
    log.Fatal(err)
}
```

Wire additional HTTP routes via `srv.Mux()`.

### Server Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithAddr(addr string)` | `":8080"` | Listen address |
| `WithMaxBody(n int64)` | `1 MiB` | Maximum request body size; set ≤ 0 to disable |
| `WithReadTimeout(d)` | `30s` | HTTP server read timeout |
| `WithWriteTimeout(d)` | `5m` | HTTP server write timeout (long to accommodate slow agents) |
| `WithIdleTimeout(d)` | `2m` | HTTP server idle timeout |
| `WithDrainTimeout(d)` | `30s` | Grace period for in-flight requests during shutdown |
| `WithLogger(l *log.Logger)` | stdlib default | Logger for server errors and operational events |
| `WithBundleClient(c *BundleClient)` | — | Enables AgentCore configuration bundle / A/B testing integration |
| `WithBundleApplier(fn BundleApplier)` | default (system prompt) | Custom function to map bundle config to the request context |

#### A/B Testing with Bundle Client

When `WithBundleClient` is configured, the server reads the W3C `baggage` header on each request, resolves the referenced configuration bundle, and applies it to the agent's invocation context before calling the agent. The default applier reads the `"system_prompt"` key and calls `ctx.WithSystemPromptOverride`. Supply `WithBundleApplier` to map additional keys:

```go
bc, _ := agentcore.NewBundleClient(runtimeARN, agentcore.WithBundleAWSConfig(awsCfg))
srv, _ := agentcore.NewServer(a,
    agentcore.WithBundleClient(bc),
    agentcore.WithBundleApplier(func(ctx *agent.Context, ref agentcore.BundleRef, cfg agentcore.BundleConfig) {
        if p := cfg.String("system_prompt"); p != "" {
            ctx.WithSystemPromptOverride(p)
        }
    }),
)
```

## Runtime

`NewRuntime` is for the AgentCore worker protocol. It registers the agent as a worker, heartbeats, long-polls for events, and submits streamed or complete responses.

```go
rt, err := agentcore.NewRuntime(myAgent,
    agentcore.WithAgentName("my-agent"),
    agentcore.WithAutoConversation(),
)
if err != nil {
    log.Fatal(err)
}
if err := rt.Run(ctx); err != nil {
    log.Fatal(err)
}
```

### Runtime Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithAWSConfig(cfg aws.Config)` | env-loaded | Explicit AWS config, bypassing environment-based loading |
| `WithAgentName(name string)` | `agent.Name()` | Agent name for worker registration with AgentCore |
| `WithHeartbeatInterval(d time.Duration)` | `5s` | Frequency of heartbeat signals to AgentCore |
| `WithShutdownTimeout(d time.Duration)` | `30s` | Grace period for in-flight events during shutdown |
| `WithStreaming(enabled bool)` | `true` | Use `InvokeStream` for incremental chunk submission |
| `WithMaxConcurrency(n int)` | `10` | Maximum events processed concurrently across sessions |
| `WithAutoConversation()` | off | Automatically create and wire an AgentCore Conversation store |
| `WithA2A(card a2a.AgentCard)` | — | Mount an A2A server on the same port; see [A2A Options](#a2a-options) |
| `WithA2AAddr(addr string)` | `":8080"` | Listen address for the A2A HTTP server |

## Conversation Store

`NewConversation` returns an `agent.Conversation` backed by AgentCore's Memory service. It stores messages as versioned JSON events, keyed by session ID. Implements `Load`, `Save`, `List`, and `Delete`.

```go
conv, err := agentcore.NewConversation(
    agentcore.WithMemoryID("my-memory-id"),
    agentcore.WithActorID("user-123"),
)
if err != nil {
    log.Fatal(err)
}

a, _ := agent.New(provider, instructions, tools,
    agent.WithSharedConversation(conv),
)
```

Wire it as a shared store when a single agent serves multiple conversations (the typical HTTP case). Use `WithAutoConversation()` on the Runtime to skip the manual wiring.

### Conversation Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithMemoryID(id string)` | `""` | AgentCore Memory instance that backs the store |
| `WithActorID(id string)` | `""` | Actor ID attached to events (identifies the user or agent that produced the event) |
| `WithConversationAWSConfig(cfg aws.Config)` | env-loaded | Explicit AWS config for the Memory service client |

## Browser Tool

`Browser` (or `NewBrowserTool` for an already-constructed client) wraps AgentCore's managed browser service as a `tool.Tool`. It supports three actions: `navigate`, `extract_content`, and `screenshot`.

```go
browser := agentcore.Browser(awsCfg,
    agentcore.WithBrowserTimeout(60*time.Second),
    agentcore.WithBrowserIdentifier("my-browser"),
)

a, _ := agent.New(provider, instructions, []tool.Tool{browser})
```

The tool is registered with the LLM under the name `"browser"`. The LLM passes a `url` (HTTP or HTTPS only) and an `action` string.

### Browser Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithBrowserTimeout(d time.Duration)` | `30s` | Maximum time to wait for a browser operation |
| `WithBrowserIdentifier(id string)` | `"default"` | Browser identifier for AgentCore sessions |

`NewBrowserTool(client, opts...)` accepts an `agentCoreClient` directly — use this when the client is already constructed or for testing.

## Code Interpreter Tool

`CodeInterpreter` (or `NewCodeInterpreterTool` for an already-constructed client) wraps AgentCore's sandboxed Python execution environment as a `tool.Tool`. The tool is registered as `"code_interpreter"`. The LLM passes a `code` string and an optional `language` field (only `"python"` is supported). Output is capped at 50,000 characters; execution time is appended as a suffix.

```go
codeInterp := agentcore.CodeInterpreter(awsCfg,
    agentcore.WithCodeTimeout(2*time.Minute),
    agentcore.WithCodeInterpreterID("my-interpreter"),
)

a, _ := agent.New(provider, instructions, []tool.Tool{codeInterp})
```

### Code Interpreter Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithCodeTimeout(d time.Duration)` | `60s` | Maximum time to wait for code execution |
| `WithCodeInterpreterID(id string)` | `"default"` | Code interpreter identifier for AgentCore sessions |

`NewCodeInterpreterTool(client, opts...)` accepts an `agentCoreClient` directly — use this when the client is already constructed or for testing.

## A2A Options

When `WithA2A` is configured on the Runtime, an A2A-compatible HTTP server is started on the same address as the AgentCore worker, serving the agent's `AgentCard` at `/.well-known/agent.json` and handling JSON-RPC requests.

```go
rt, _ := agentcore.NewRuntime(a,
    agentcore.WithAgentName("my-agent"),
    agentcore.WithA2A(a2a.AgentCard{
        Name:        "My Agent",
        Description: "An assistant deployed on AgentCore with A2A support",
        URL:         "https://my-agent.example.com",
    }),
    agentcore.WithA2AAddr(":8080"),
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithA2A(card a2a.AgentCard)` | — | Mount an A2A server; the provided `AgentCard` is advertised at `/.well-known/agent.json` |
| `WithA2AAddr(addr string)` | `":8080"` | Listen address for the combined A2A + AgentCore HTTP server |

Both options take effect on `Runtime` only (not `Server`). The A2A server shares the runtime's shutdown sequence — it stops when `Run` returns.

## Observability

`SetupTracing` wires an OpenTelemetry tracer provider configured for AgentCore's observability dashboards. It sets `service.name = {RuntimeName}.DEFAULT` so the Recommendations API can discover traces in CloudWatch.

```go
shutdown, tracingOpt, err := agentcore.SetupTracing(ctx, "MyAssistant",
    agentcore.WithOTLPInsecure(), // required for the in-container ADOT sidecar
)
defer shutdown(context.Background())

a, _ := agent.New(provider, instructions, tools, tracingOpt)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithOTLPEndpoint(endpoint string)` | `OTEL_EXPORTER_OTLP_ENDPOINT` or `localhost:4317` | OTLP collector endpoint |
| `WithOTLPHTTP()` | off (gRPC) | Use the HTTP OTLP exporter instead of gRPC |
| `WithOTLPInsecure()` | off | Disable TLS; required for the in-container ADOT sidecar |
| `WithTracingContentCapture()` | off | Capture prompts and completions on spans; disable in production if messages contain PII |

## Code Example

A complete agent deployed to AgentCore using the HTTP server:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os/signal"
    "syscall"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/agentcore"
    "github.com/camilbinas/gude-agents/agent/logging/auto"
    "github.com/camilbinas/gude-agents/agent/prompt"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
    "github.com/camilbinas/gude-agents/agent/tool"
)

type WeatherInput struct {
    City string `json:"city" description:"City to get weather for" required:"true"`
}

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    awsCfg, err := config.LoadDefaultConfig(ctx)
    if err != nil {
        log.Fatalf("aws config: %v", err)
    }

    shutdown, tracingOpt, err := agentcore.SetupTracing(ctx, "WeatherAgent",
        agentcore.WithOTLPInsecure(),
    )
    if err != nil {
        log.Fatalf("tracing: %v", err)
    }
    defer shutdown(context.Background())

    weather := tool.New("get_weather", "Get the current weather for a city.",
        func(_ context.Context, in WeatherInput) (string, error) {
            return fmt.Sprintf("Weather in %s: 22°C, partly cloudy", in.City), nil
        },
    )
    codeInterp := agentcore.CodeInterpreter(awsCfg)

    a, err := agent.New(
        bedrock.Must(bedrock.Standard()),
        prompt.Text("You are a helpful assistant. Use code_interpreter for calculations."),
        []tool.Tool{weather, codeInterp},
        agent.WithName("WeatherAgent"),
        auto.WithLogging(),
        tracingOpt,
    )
    if err != nil {
        log.Fatal(err)
    }
    defer a.Close()

    srv, err := agentcore.NewServer(a)
    if err != nil {
        log.Fatal(err)
    }

    if err := srv.ListenAndServe(ctx); err != nil {
        log.Fatalf("server: %v", err)
    }
}
```

See `examples/agentcore-deploy` for a full deployment example including the Dockerfile, deploy script, and A/B testing with configuration bundles.

## See Also

- [Agent-to-Agent (A2A) Protocol](a2a.md) — expose agents and call remote agents with the A2A protocol
- [Conversation System](conversation.md) — wiring conversation stores with `WithConversation` and `WithSharedConversation`
- [Tool System](tools.md) — building and using tools
- [Tracing](tracing.md) — general OpenTelemetry tracing options
- [Getting Started](getting-started.md) — installation and first agent
