# Structured Logging

The `agent/logging/slog` module adds structured logging to gude-agents using the standard library's `log/slog` package. It lives in a separate Go module with its own `go.mod`, keeping the core `agent/` package free of logging implementation dependencies. You opt in by importing the logging submodule and passing `slog.WithLogging()` as an `agent.Option`.

The module instruments the full agent lifecycle: invocations, loop iterations, provider calls, tool executions, guardrails, memory operations, RAG retrieval, graph workflows, and swarm coordination.

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

## Option Functions

### `WithLogging`

```go
func WithLogging(opts ...Option) agent.Option
```

Returns an `agent.Option` that creates a slog-based logging hook and installs it on the agent. The hook emits structured log entries at each lifecycle point using `log/slog`.

### `WithGraphLogging`

```go
func WithGraphLogging(opts ...Option) graph.GraphOption
```

Returns a `graph.GraphOption` that installs the slog-based logging hook on a graph.

### `WithSwarmLogging`

```go
func WithSwarmLogging(opts ...Option) agent.SwarmOption
```

Returns an `agent.SwarmOption` that installs the slog-based logging hook on a swarm.

### `WithHandler`

```go
func WithHandler(h slog.Handler) Option
```

Sets a custom `slog.Handler` for log output. When not provided, the hook uses `slog.Default()`.

```go
a, err := agent.New(provider, instructions, tools,
    agentslog.WithLogging(agentslog.WithHandler(slog.NewJSONHandler(os.Stdout, nil))),
)
```

### `WithMinLevel`

```go
func WithMinLevel(level slog.Level) Option
```

Sets the minimum log level. Entries below this level are not emitted. Default is `slog.LevelDebug` (all entries).

```go
a, err := agent.New(provider, instructions, tools,
    agentslog.WithLogging(agentslog.WithMinLevel(slog.LevelInfo)),
)
```

## Log Level Mapping

Each lifecycle point maps to a log level:

| Lifecycle Point | Default Level | With Error |
|---|---|---|
| InvokeStart, IterationStart, ProviderCallStart, ToolStart, MemoryStart, RetrieverStart | Debug | — |
| InvokeEnd, ProviderCallEnd, ToolEnd, MemoryEnd, RetrieverEnd | Info | Error |
| GuardrailComplete (not blocked) | Debug | Error |
| GuardrailComplete (blocked) | Warn | Error |
| MaxIterationsExceeded | Warn | — |

For graph and swarm hooks:

| Lifecycle Point | Default Level | With Error |
|---|---|---|
| GraphRunStart, NodeStart, SwarmRunStart, SwarmAgentStart | Debug | — |
| GraphRunEnd, NodeEnd, SwarmRunEnd, SwarmAgentEnd | Info | Error |
| SwarmHandoff | Info | — |

Start events are Debug because they fire frequently and are mainly useful for detailed debugging. End events are Info because they carry outcome data (duration, token usage). Errors always escalate to Error level.

## Structured Attributes

Each log entry includes relevant key-value attributes:

| Attribute | Events | Description |
|---|---|---|
| `agent.name` | InvokeStart | Agent name (when set via `WithName`) |
| `model.id` | InvokeStart, ProviderCallStart | Provider model ID |
| `conversation_id` | InvokeStart, MemoryStart, MemoryEnd | Memory conversation ID |
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
| `operation` | MemoryStart, MemoryEnd | Memory operation (`load` or `save`) |
| `message_count` | MemoryEnd | Number of messages loaded or saved |
| `direction` | GuardrailComplete | Guardrail direction (`input` or `output`) |
| `blocked` | GuardrailComplete | Whether the guardrail blocked |
| `initial_agent` / `member_count` / `max_handoffs` | SwarmRunStart | Swarm configuration |
| `final_agent` / `handoff_count` | SwarmRunEnd | Swarm outcome |

## Relationship to Legacy Logger

The `LoggingHook` replaces the old `WithLogger` / `logf` pattern that was removed in v0.3.0. All lifecycle logging now goes through the hook system. When no hook is set, no logging occurs.

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

## Coexistence with Tracing and Metrics

Logging, tracing, and metrics can all be enabled simultaneously:

```go
import (
    "github.com/camilbinas/gude-agents/agent/tracing"
    prometheus "github.com/camilbinas/gude-agents/agent/metrics/prometheus"
    agentslog "github.com/camilbinas/gude-agents/agent/logging/slog"
)

a, err := agent.New(provider, instructions, tools,
    tracing.WithTracing(nil),
    prometheus.WithMetrics(),
    agentslog.WithLogging(),
)
```

All three hooks are separate fields on the `Agent` struct. The agent loop nil-checks each independently. The logging hook does not modify context (unlike the tracing hook which injects spans), so there is no ordering dependency.

## See Also

- [OpenTelemetry Tracing](tracing.md) — distributed tracing with spans
- [Prometheus Metrics](metrics.md) — counters and histograms
- [OTEL Metrics](metrics-otel.md) — OpenTelemetry metrics exporter
- [CloudWatch Metrics](metrics-cloudwatch.md) — AWS CloudWatch metrics exporter
- [Agent API Reference](agent-api.md) — constructor, options, and invoke methods
