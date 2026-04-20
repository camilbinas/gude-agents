# OTEL Metrics

The `agent/metrics/otel` module adds OpenTelemetry counters and histograms to gude-agents. It lives in a separate Go module with its own `go.mod`, keeping the core `agent/` package free of OTEL metrics dependencies. You opt in by importing the metrics submodule and passing `otel.WithMetrics()` as an `agent.Option`.

The module records the same 9 metrics as the [Prometheus exporter](metrics.md) — 6 counters and 3 histograms — using OTEL metric instruments. Metrics export to any OTLP-compatible backend (Datadog, Grafana Cloud, New Relic, etc.) via the user-supplied `MeterProvider`.

## Enabling Metrics

Pass a `MeterProvider` and `otel.WithMetrics` as an agent option:

```go
import (
    "github.com/camilbinas/gude-agents/agent"
    otelmetrics "github.com/camilbinas/gude-agents/agent/metrics/otel"
)

a, err := agent.New(provider, instructions, tools,
    otelmetrics.WithMetrics(meterProvider),
)
```

If `meterProvider` is nil, the global `MeterProvider` is used via `otel.GetMeterProvider()`. If no global provider is set, metrics are silently dropped (OTEL no-op behavior).

## Option Functions

### `WithMetrics`

```go
func WithMetrics(mp metric.MeterProvider, opts ...Option) agent.Option
```

Returns an `agent.Option` that creates an OTEL metrics hook, registers all metric instruments from the provided `MeterProvider`, and installs the hook on the agent.

### `WithNamespace`

```go
func WithNamespace(ns string) Option
```

Sets the meter instrumentation scope name. When not set, the default `github.com/camilbinas/gude-agents` is used. For example, `WithNamespace("myapp.agent")` creates instruments under that scope.

## Metric Reference

### Counters

| Instrument Name | Attributes | Description |
|---|---|---|
| `agent.invoke.total` | `status`, `agent_name` | Total invocations |
| `agent.provider.call.total` | `model_id`, `status`, `agent_name` | Total provider calls |
| `agent.provider.tokens.total` | `model_id`, `direction`, `agent_name` | Token consumption |
| `agent.tool.call.total` | `tool_name`, `status`, `agent_name` | Total tool executions |
| `agent.guardrail.block.total` | `direction`, `agent_name` | Guardrail rejections |
| `agent.iteration.total` | `agent_name` | Total loop iterations |

### Histograms

| Instrument Name | Attributes | Description |
|---|---|---|
| `agent.invoke.duration` | `agent_name` | Invocation duration (seconds) |
| `agent.provider.call.duration` | `agent_name` | Provider call duration (seconds) |
| `agent.tool.call.duration` | `tool_name`, `agent_name` | Tool execution duration (seconds) |

All histograms use explicit bucket boundaries: 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, and 120.0 seconds.

The `agent_name` attribute is only present when `agent.WithName()` is set on the agent.

## Attribute Values

| Attribute | Values | Source |
|---|---|---|
| `status` | `"success"`, `"error"` | `err == nil` in finish function |
| `model_id` | Model string or `"unknown"` | From `ModelIdentifier`; falls back to `"unknown"` |
| `tool_name` | Tool name string | From the tool call name |
| `direction` | `"input"`, `"output"` | Guardrail direction or token direction |
| `agent_name` | Agent name string | From `WithName` option |

## See Also

- [Prometheus Metrics](metrics.md) — Prometheus counters and histograms
- [CloudWatch Metrics](metrics-cloudwatch.md) — AWS CloudWatch metrics exporter
- [OpenTelemetry Tracing](tracing.md) — distributed tracing with spans
- [Agent API Reference](agent-api.md) — constructor, options, and invoke methods
