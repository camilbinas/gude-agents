# CloudWatch Metrics

The `agent/metrics/cloudwatch` module publishes agent lifecycle metrics to AWS CloudWatch as custom metrics. It lives in a separate Go module with its own `go.mod`, keeping the core `agent/` package free of AWS SDK dependencies. You opt in by importing the metrics submodule and calling `cloudwatch.WithMetrics()`.

The module records the same 9 metrics as the [Prometheus exporter](metrics.md) — 6 counters and 3 duration metrics (as StatisticSets). Metrics are buffered in memory and flushed to CloudWatch periodically via `PutMetricData`.

## Enabling Metrics

Call `cloudwatch.WithMetrics` to get an agent option and a shutdown function:

```go
import (
    "github.com/camilbinas/gude-agents/agent"
    cloudwatch "github.com/camilbinas/gude-agents/agent/metrics/cloudwatch"
)

metricsOpt, shutdown := cloudwatch.WithMetrics(
    cloudwatch.WithNamespace("MyApp"),
)
defer shutdown(ctx)

a, err := agent.New(provider, instructions, tools, metricsOpt)
```

The shutdown function stops the background flush goroutine and performs a final flush of buffered data points. Always defer it to avoid losing metrics on exit.

## Option Functions

### `WithMetrics`

```go
func WithMetrics(opts ...Option) (agent.Option, func(context.Context) error)
```

Returns an `agent.Option` that creates a CloudWatch metrics hook and a shutdown function. The hook initializes the CloudWatch client using the AWS SDK v2 default credential chain (unless `WithClient` is provided), starts a background flush goroutine, and installs the hook on the agent.

### `WithNamespace`

```go
func WithNamespace(ns string) Option
```

Sets the CloudWatch namespace for all published metrics. Default: `"GudeAgents"`.

### `WithClient`

```go
func WithClient(c *cloudwatch.Client) Option
```

Sets a pre-configured CloudWatch client, bypassing the default credential chain initialization. Useful for custom endpoints or testing.

### `WithFlushInterval`

```go
func WithFlushInterval(d time.Duration) Option
```

Sets the time between flush cycles. Default: 60 seconds.

### `WithDimensions`

```go
func WithDimensions(dims map[string]string) Option
```

Adds extra key-value pairs as dimensions to all published metrics. For example, `WithDimensions(map[string]string{"Environment": "production"})` adds an `Environment` dimension to every data point.

## Metric Reference

### Counters

| Metric Name | Dimensions | Description |
|---|---|---|
| `AgentInvokeTotal` | `Status`, `AgentName` | Total invocations |
| `AgentProviderCallTotal` | `ModelId`, `Status`, `AgentName` | Total provider calls |
| `AgentProviderTokensTotal` | `ModelId`, `Direction`, `AgentName` | Token consumption |
| `AgentToolCallTotal` | `ToolName`, `Status`, `AgentName` | Total tool executions |
| `AgentGuardrailBlockTotal` | `Direction`, `AgentName` | Guardrail rejections |
| `AgentIterationTotal` | `AgentName` | Total loop iterations |

### Duration Metrics (StatisticSets)

| Metric Name | Unit | Dimensions | Description |
|---|---|---|---|
| `AgentInvokeDuration` | Seconds | `AgentName` | Invocation duration |
| `AgentProviderCallDuration` | Seconds | `AgentName` | Provider call duration |
| `AgentToolCallDuration` | Seconds | `ToolName`, `AgentName` | Tool execution duration |

The `AgentName` dimension is only present when `agent.WithName()` is set on the agent.

## Dimension Values

| Dimension | Values | Source |
|---|---|---|
| `Status` | `"success"`, `"error"` | `err == nil` in finish function |
| `ModelId` | Model string or `"unknown"` | From `ModelIdentifier`; falls back to `"unknown"` |
| `ToolName` | Tool name string | From the tool call name |
| `Direction` | `"input"`, `"output"` | Guardrail direction or token direction |
| `AgentName` | Agent name string | From `WithName` option |

## Flush Behavior

- Data points are buffered in memory between flushes
- Each `PutMetricData` call sends at most 1000 data points (CloudWatch API limit)
- Larger buffers are split into multiple API calls
- Failed `PutMetricData` calls log the error and retain data points for the next flush
- Call `Flush(ctx)` on the hook for an immediate flush outside the regular cycle

## Shutdown

The shutdown function returned by `WithMetrics` stops the background flush goroutine and performs a final flush:

```go
metricsOpt, shutdown := cloudwatch.WithMetrics()
defer shutdown(ctx)
```

The shutdown blocks until the final flush completes or the provided context is cancelled.

## See Also

- [Prometheus Metrics](metrics.md) — Prometheus counters and histograms
- [OTEL Metrics](metrics-otel.md) — OpenTelemetry metrics exporter
- [OpenTelemetry Tracing](tracing.md) — distributed tracing with spans
- [Agent API Reference](agent-api.md) — constructor, options, and invoke methods
