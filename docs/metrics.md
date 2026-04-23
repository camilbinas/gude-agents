# Prometheus Metrics

The `agent/metrics/prometheus` module adds Prometheus counters and histograms to gude-agents. It lives in a separate Go module with its own `go.mod`, keeping the core `agent/` package free of Prometheus dependencies. You opt in by importing the metrics submodule and passing `prometheus.WithMetrics()` as an `agent.Option`.

The module instruments the agent lifecycle: invocations, loop iterations, provider calls, tool executions, guardrail blocks, and token consumption.

## Enabling Metrics

Pass `prometheus.WithMetrics` as an agent option:

```go
import (
    "github.com/camilbinas/gude-agents/agent"
    prometheus "github.com/camilbinas/gude-agents/agent/metrics/prometheus"
)

a, err := agent.New(provider, instructions, tools,
    prometheus.WithMetrics(),
)
```

When metrics are not enabled, the agent performs nil checks at each hook call site and skips all metrics logic. No counters, histograms, or registries are allocated.

## Quick Start with HTTP Handler

The most common setup is enabling metrics and exposing an HTTP endpoint for Prometheus to scrape. `NewHandler` returns both the agent option and an `http.Handler` in one call:

```go
package main

import (
    "log"
    "net/http"

    "github.com/camilbinas/gude-agents/agent"
    prometheus "github.com/camilbinas/gude-agents/agent/metrics/prometheus"
)

func main() {
    metricsOpt, metricsHandler := prometheus.NewHandler(
        prometheus.WithNamespace("myapp"),
    )

    http.Handle("/metrics", metricsHandler)
    go http.ListenAndServe(":2112", nil)

    a, err := agent.New(provider, instructions, tools, metricsOpt)
    if err != nil {
        log.Fatal(err)
    }

    // Use the agent — metrics accumulate automatically.
    a.Invoke(ctx, "Hello")
}
```

Then scrape with `curl localhost:2112/metrics` or configure Prometheus:

```yaml
scrape_configs:
  - job_name: gude-agents
    static_configs:
      - targets: ['localhost:2112']
```

## Option Functions

### `WithMetrics`

```go
func WithMetrics(opts ...Option) agent.Option
```

Returns an `agent.Option` that creates a Prometheus metrics hook, registers all metrics, and installs the hook on the agent. Accepts optional configuration via `Option` functions.

When no `WithRegisterer` option is provided, metrics are registered with `prometheus.DefaultRegisterer`.

### `NewHandler`

```go
func NewHandler(opts ...Option) (agent.Option, http.Handler)
```

Convenience function that creates the metrics hook eagerly and returns both the `agent.Option` (to pass to `agent.New`) and an `http.Handler` (to mount on your HTTP server). The handler serves metrics in standard Prometheus exposition format.

The hook is created immediately so the handler is usable before `agent.New` is called. When no custom registerer is provided, `NewHandler` creates a fresh `prometheus.Registry` (not the default global one), so the handler only serves agent metrics.

### `WithNamespace`

```go
func WithNamespace(ns string) Option
```

Sets a prefix for all metric names. For example, `WithNamespace("myapp")` produces `myapp_agent_invoke_total` instead of `agent_invoke_total`.

### `WithRegisterer`

```go
func WithRegisterer(r prometheus.Registerer) Option
```

Sets a custom Prometheus registerer. When not provided, `WithMetrics` uses `prometheus.DefaultRegisterer` and `NewHandler` creates a fresh registry.

If the registerer also implements `prometheus.Gatherer` (as `prometheus.NewRegistry()` does), it is used as the gatherer for the HTTP handler.

## Metric Reference

### Counters

| Metric Name | Labels | Description |
|---|---|---|
| `agent_invoke_total` | `status` | Total invocations (incremented on completion) |
| `agent_provider_call_total` | `model_id`, `status` | Total provider calls |
| `agent_provider_tokens_total` | `model_id`, `direction` | Token consumption (input/output) |
| `agent_tool_call_total` | `tool_name`, `status` | Total tool executions |
| `agent_guardrail_block_total` | `direction` | Guardrail rejections (only incremented when blocked) |
| `agent_iteration_total` | — | Total loop iterations |
| `agent_images_attached_total` | — | Total images attached via `WithImages` (incremented by image count per invocation) |

### Histograms

| Metric Name | Labels | Description |
|---|---|---|
| `agent_invoke_duration_seconds` | — | Invocation duration |
| `agent_provider_call_duration_seconds` | — | Provider call duration |
| `agent_tool_call_duration_seconds` | `tool_name` | Tool execution duration |

All histograms use buckets tuned for LLM latencies: 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, and 120.0 seconds.

## Label Values

| Label | Values | Source |
|---|---|---|
| `status` | `"success"`, `"error"` | Determined by whether the finish function receives a nil or non-nil error |
| `model_id` | Provider model string or `"unknown"` | From `ModelIdentifier` interface; falls back to `"unknown"` if the provider doesn't implement it |
| `tool_name` | Tool name string | From the tool call name |
| `direction` | `"input"`, `"output"` | Guardrail direction or token direction |
| `agent_name` | Agent name string | From `WithName` option; only present when set |

## Agent Name

When `agent.WithName("my-agent")` is set, the Prometheus exporter adds `agent_name` as a const label on all metrics. This makes it possible to distinguish metrics from different agents in the same process.

```go
a, err := agent.New(provider, instructions, tools,
    agent.WithName("order-agent"),
    prometheus.WithMetrics(),
)
```

Note: `NewHandler` creates metrics eagerly before the agent exists, so the `agent_name` label is only supported via the `WithMetrics` path.

## Hook Lifecycle

The metrics hook is called at the same lifecycle points as the tracing hook. Both hooks are independent — if one is nil, the other still fires.

```
InvokeStream
├── OnInvokeStart → finishInvoke(err, usage)
│   ├── guardrail (input) → OnGuardrailComplete("input", blocked)
│   └── runLoop
│       ├── OnIterationStart
│       ├── OnProviderCallStart(modelID) → finishProvider(err, usage)
│       ├── OnToolStart(toolName) → finishTool(err)  [per tool]
│       └── guardrail (output) → OnGuardrailComplete("output", blocked)
└── finishInvoke called
```

Each `On*Start` method captures the start time in a closure and returns a finish function. The finish function records the duration and outcome when called.

## Coexistence with Tracing

Metrics and tracing can be enabled simultaneously:

```go
import (
    "github.com/camilbinas/gude-agents/agent/tracing"
    prometheus "github.com/camilbinas/gude-agents/agent/metrics/prometheus"
)

a, err := agent.New(provider, instructions, tools,
    tracing.WithTracing(nil),
    prometheus.WithMetrics(),
)
```

Both hooks are separate fields on the `Agent` struct (alongside the logging hook). The agent loop nil-checks each independently. The metrics hook does not modify context (unlike the tracing hook which injects spans), so there is no ordering dependency.

## Custom Registerer for Testing

In tests, use a fresh registry to avoid cross-test metric collisions:

```go
reg := prometheus.NewRegistry()
a, err := agent.New(provider, instructions, tools,
    prometheus.WithMetrics(prometheus.WithRegisterer(reg)),
)

// After invocation, gather metrics from the test registry.
families, _ := reg.Gather()
```

## Swarm and Graph Metrics

The core `agent` package also defines `SwarmMetricsHook` and `GraphMetricsHook` interfaces for swarm-level and graph-level metrics (handoff counts, agent turn durations, node execution durations, graph run durations). These are wired into `Swarm.Run` and `Graph.Run` alongside the existing tracing and logging hooks.

Metrics exporters can implement these interfaces to add orchestration-level counters and histograms on top of the per-agent metrics that `MetricsHook` already provides.

## See Also

- [Structured Logging](logging.md) — `log/slog`-based structured logging
- [OTEL Metrics](metrics-otel.md) — OpenTelemetry metrics exporter
- [CloudWatch Metrics](metrics-cloudwatch.md) — AWS CloudWatch metrics exporter
- [OpenTelemetry Tracing](tracing.md) — distributed tracing with spans
- [Agent API Reference](agent-api.md) — constructor, options, and invoke methods
- [Guardrails](guardrails.md) — input/output guardrails that produce guardrail block metrics
- [Middleware](middleware.md) — wrapping tool execution
