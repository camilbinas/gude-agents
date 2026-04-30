# Structured Logging

The `agent/logging/slog` module adds structured logging to gude-agents using the standard library's `log/slog` package. It lives in a separate Go module with its own `go.mod`, keeping the core `agent/` package free of logging implementation dependencies. You opt in by importing the logging submodule and passing `slog.WithLogging()` as an `agent.Option`.

The module instruments the full agent lifecycle: invocations, loop iterations, provider calls, tool executions, guardrails, conversation operations, RAG retrieval, graph workflows, and swarm coordination.

## Enabling Logging

Pass `agentslog.WithLogging` as an agent option:

```go
import (
    "github.com/camilbinas/gude-agents/agent"
    agentslog "github.com/camilbinas/gude-agents/agent/logging/slog"
)

a, err := agent.New(provider, instructions, tools,
    agentslog.WithLogging(),
)
```

When no logging hook is set, the agent performs nil checks at each hook call site and skips all logging hook logic.

## Auto Logging

Import: `github.com/camilbinas/gude-agents/agent/logging/auto`

Selects the backend based on the environment. Checks `APP_ENV`, `ENV`, and `ENVIRONMENT` in order (first non-empty wins). Defaults to production-safe structured logging.

```go
import "github.com/camilbinas/gude-agents/agent/logging/auto"

a, err := agent.New(provider, instructions, tools,
    auto.WithLogging(),
)
```

| Environment value | Backend |
|-----------|---------|
| `development`, `dev`, or `local` (case-insensitive) | `debug.WithLogging()` |
| anything else / unset | `slog.WithLogging()` |

`auto.WithGraphLogging()` follows the same logic for graph workflows.

## Option Functions

| Function | Returns | Description |
|----------|---------|-------------|
| `WithLogging(opts...)` | `agent.Option` | Installs slog-based logging hook on an agent |
| `WithGraphLogging(opts...)` | `graph.GraphOption` | Installs slog-based logging hook on a graph |
| `WithSwarmLogging(opts...)` | `agent.SwarmOption` | Installs slog-based logging hook on a swarm |
| `WithHandler(h slog.Handler)` | `Option` | Sets a custom slog handler (default: `slog.Default()`) |
| `WithMinLevel(level slog.Level)` | `Option` | Minimum log level (default: `slog.LevelDebug`) |

```go
a, err := agent.New(provider, instructions, tools,
    agentslog.WithLogging(
        agentslog.WithHandler(slog.NewJSONHandler(os.Stdout, nil)),
        agentslog.WithMinLevel(slog.LevelInfo),
    ),
)
```

## Log Level Mapping

Each lifecycle point maps to a log level:

| Lifecycle Point | Default Level | With Error |
|---|---|---|
| InvokeStart, IterationStart, ProviderCallStart, ToolStart, ConversationStart, RetrieverStart, ImagesAttached, DocumentsAttached | Debug | — |
| InvokeEnd, ProviderCallEnd, ToolEnd, ConversationEnd, RetrieverEnd | Info | Error |
| GuardrailComplete (not blocked) | Debug | Error |
| GuardrailComplete (blocked) | Warn | Error |
| MaxIterationsExceeded | Warn | — |

For graph and swarm hooks:

| Lifecycle Point | Default Level | With Error |
|---|---|---|
| GraphRunStart, NodeStart, SwarmRunStart, SwarmAgentStart | Debug | — |
| GraphRunEnd, NodeEnd, SwarmRunEnd, SwarmAgentEnd | Info | Error |
| SwarmHandoff | Info | — |

## Structured Attributes

Each log entry includes relevant key-value attributes:

| Attribute | Events | Description |
|---|---|---|
| `agent.name` | InvokeStart | Agent name (when set via `WithName`) |
| `model.id` | InvokeStart, ProviderCallStart | Provider model ID |
| `conversation_id` | InvokeStart, ConversationStart, ConversationEnd | Conversation ID |
| `max_iterations` | InvokeStart | Configured max iterations |
| `iteration` | IterationStart | 1-based iteration number |
| `tool.name` | ToolStart, ToolEnd | Tool being executed |
| `node.name` | NodeStart, NodeEnd | Graph node name |
| `agent.from` / `agent.to` | SwarmHandoff | Handoff source and target |
| `duration_ms` | All end events | Operation duration in milliseconds |
| `error` | End events with error | Error message |
| `input_tokens` / `output_tokens` | InvokeEnd, ProviderCallEnd, GraphRunEnd | Token usage |
| `tool_call_count` | ProviderCallEnd | Number of tool calls in provider response |
| `doc_count` | RetrieverEnd | Number of retrieved documents |
| `image_count` | ImagesAttached | Number of images attached via `WithImages` |
| `document_count` | DocumentsAttached | Number of documents attached via `WithDocuments` |
| `operation` | ConversationStart, ConversationEnd | Conversation operation (`load` or `save`) |
| `message_count` | ConversationEnd | Number of messages loaded or saved |
| `direction` | GuardrailComplete | Guardrail direction (`input` or `output`) |
| `blocked` | GuardrailComplete | Whether the guardrail blocked |
| `initial_agent` / `member_count` / `max_handoffs` | SwarmRunStart | Swarm configuration |
| `final_agent` / `handoff_count` | SwarmRunEnd | Swarm outcome |

## Graph Logging

```go
import (
    "github.com/camilbinas/gude-agents/agent/graph"
    agentslog "github.com/camilbinas/gude-agents/agent/logging/slog"
)

g, err := graph.NewGraph(
    agentslog.WithGraphLogging(),
)
```

Graph logging emits entries for `graph.run.start`, `graph.run.end`, `graph.node.start`, and `graph.node.end`.

## Swarm Logging

```go
import (
    "github.com/camilbinas/gude-agents/agent"
    agentslog "github.com/camilbinas/gude-agents/agent/logging/slog"
)

swarm, err := agent.NewSwarm(members,
    agentslog.WithSwarmLogging(),
)
```

Swarm logging emits entries for `swarm.run.start`, `swarm.run.end`, `swarm.agent.start`, `swarm.agent.end`, and `swarm.handoff`.

## Coexistence with Tracing, Metrics, and Event Hook

Logging, tracing, metrics, and the event hook can all be enabled simultaneously. Tracing, metrics, and logging are agent-scoped (set at construction); the event hook is invocation-scoped (set via context):

```go
a, err := agent.New(provider, instructions, tools,
    tracing.WithTracing(nil),
    prometheus.WithMetrics(),
    agentslog.WithLogging(),
)

ctx = agent.WithEventHook(ctx, myUIHook)
a.InvokeStream(ctx, message, streamCB)
```

All hooks are nil-checked independently. The logging hook does not modify context (unlike the tracing hook which injects spans), so there is no ordering dependency.

## See Also

- [OpenTelemetry Tracing](tracing.md) — distributed tracing with spans
- [Prometheus Metrics](metrics.md) — counters and histograms
- [OTEL Metrics](metrics-otel.md) — OpenTelemetry metrics exporter
- [CloudWatch Metrics](metrics-cloudwatch.md) — AWS CloudWatch metrics exporter
- [Agent API Reference](agent-api.md) — constructor, options, and invoke methods
