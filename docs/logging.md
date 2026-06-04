# Structured Logging

The `agent/logging/slog` module adds structured logging to gude-agents using the standard library's `log/slog` package. It lives in a separate Go module with its own `go.mod`, keeping the core `agent/` package free of logging implementation dependencies. You opt in by importing the logging submodule and passing `slog.WithLogging()` as an `agent.Option`.

The module instruments the full agent lifecycle: invocations, loop iterations, provider calls, tool executions, guardrails, conversation operations, RAG retrieval, and graph workflows.

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
| InvokeStart, IterationStart, IterationEnd, ProviderCallStart, ToolStart, ConversationStart, RetrieverStart, ImagesAttached, DocumentsAttached | Debug | — |
| InvokeEnd, ProviderCallEnd, ToolEnd, ConversationEnd, RetrieverEnd | Info | Error |
| GuardrailComplete (not blocked) | Debug | Error |
| GuardrailComplete (blocked) | Warn | Error |
| MaxIterationsExceeded | Warn | — |

For graph hooks:

| Lifecycle Point | Default Level | With Error |
|---|---|---|
| GraphRunStart, NodeStart | Debug | — |
| GraphRunEnd, NodeEnd | Info | Error |

## Structured Attributes

Each log entry includes relevant key-value attributes:

| Attribute | Events | Description |
|---|---|---|
| `agent.name` | InvokeStart | Agent name (when set via `WithName`) |
| `model.id` | InvokeStart, ProviderCallStart | Provider model ID |
| `conversation_id` | InvokeStart, ConversationStart, ConversationEnd | Conversation ID |
| `max_iterations` | InvokeStart | Configured max iterations |
| `iteration` | IterationStart | 1-based iteration number |
| `tool.name` | ToolStart, ToolEnd, ToolLog | Tool being executed |
| `node.name` | NodeStart, NodeEnd | Graph node name |
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

## Tool Logging

Tools can emit log messages during execution via `agent.ToolLoggerFrom(ctx)`. The logger is automatically injected when a `LoggingHook` is configured.

`tool.WithLogger(ctx context.Context, l tool.Logger) context.Context` injects a logger directly into a context. Use this in tool handlers that construct their own context and don't have access to the agent's `LoggingHook` — for example, when calling a sub-tool manually or passing context into a helper that expects a logger already attached.

```go
func myTool(ctx context.Context, input MyInput) (string, error) {
    log := agent.ToolLoggerFrom(ctx)
    log.Logf("searching for %q", input.Query)
    results := doSearch(input.Query)
    log.Logf("found %d results", len(results))
    return formatResults(results), nil
}
```

`ToolLoggerFrom` returns a no-op logger when no hook is configured, so tools can call it unconditionally.

## Stream and Response Logging

The logging hook receives stream chunks and final responses automatically. When a `LoggingHook` is configured, `OnStreamChunk` is called for each final-answer chunk during `InvokeStream`, and `OnResponse` is called with the complete text after a non-streaming `Invoke`. The user's `StreamCallback` is still called if provided — the hook is additive.

The debug logger prints stream chunks to stdout. The slog logger only logs `OnResponse` (chunks are too noisy for structured logs).

## Graph Logging

```go
import (
    "github.com/camilbinas/gude-agents/agent/graph"
    agentslog "github.com/camilbinas/gude-agents/agent/logging/slog"
)

g, err := graph.New[graph.State](
    agentslog.WithGraphLogging(),
)
```

Graph logging emits entries for `graph.run.start`, `graph.run.end`, `graph.node.start`, and `graph.node.end`.

## Coexistence with Tracing, Metrics, and Event Hook

Logging, tracing, metrics, and the event hook can all be enabled simultaneously. Tracing, metrics, and logging are agent-scoped (set at construction); the event hook is invocation-scoped (set via `*Context`):

```go
a, err := agent.New(provider, instructions, tools,
    tracing.WithTracing(nil),
    prometheus.WithMetrics(),
    agentslog.WithLogging(),
)

c := agent.Background().WithEventHook(myUIHook)
a.InvokeStream(c, message, streamCB)
```

All hooks are nil-checked independently. The logging hook does not modify context (unlike the tracing hook which injects spans), so there is no ordering dependency.

## See Also

- [OpenTelemetry Tracing](tracing.md) — distributed tracing with spans
- [Prometheus Metrics](metrics.md) — counters and histograms
- [OTEL Metrics](metrics-otel.md) — OpenTelemetry metrics exporter
- [CloudWatch Metrics](metrics-cloudwatch.md) — AWS CloudWatch metrics exporter
- [Agent API Reference](agent-api.md) — constructor, options, and invoke methods
