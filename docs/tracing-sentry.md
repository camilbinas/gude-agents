# Sentry Integration

The `agent/tracing/sentry` module combines OTEL trace export with Sentry's error capture and breadcrumb features. Only the DSN is needed — the OTLP endpoint is derived automatically.

```go
import (
    sentrytrace "github.com/camilbinas/gude-agents/agent/tracing/sentry"
    "github.com/camilbinas/gude-agents/agent/tracing"
)

shutdown, _ := sentrytrace.Setup(ctx, sentrytrace.Config{
    DSN: "https://key@o123.ingest.us.sentry.io/456",
})
defer shutdown(ctx)

a, _ := agent.New(provider, instructions, tools,
    sentrytrace.WithSentry(),
    agent.WithMiddleware(
        sentrytrace.BreadcrumbMiddleware(),
        sentrytrace.ErrorCaptureMiddleware(),
    ),
)
```

## API

| Function | Description |
|----------|-------------|
| `Setup(ctx, Config)` | Initializes Sentry SDK + OTLP HTTP exporter pointed at Sentry's endpoint |
| `WithSentry(opts...)` | Wraps `tracing.WithTracing()` using the global TracerProvider from Setup |
| `ErrorCaptureMiddleware()` | Captures tool errors as Sentry Issues linked to the active OTEL trace |
| `BreadcrumbMiddleware()` | Adds a breadcrumb for every tool call (visible in Issue detail) |
| `CaptureAgentError(ctx, err, msg, usage)` | Manually capture invocation-level errors with classification and token usage |

## Error Classification

Errors captured via `CaptureAgentError` are tagged with `agent.error_type`:

| Tag Value | Condition |
|-----------|-----------|
| `provider_error` | LLM provider failures |
| `tool_error` | Tool execution failures |
| `guardrail_error` | Guardrail rejections |
| `token_budget_exceeded` | Token budget exceeded |
| `max_iterations_exceeded` | Iteration limit hit |

## Content Capture

Pass `tracing.WithContentCapture()` to include prompts, responses, and tool I/O in span attributes. Useful for debugging but adds data volume — avoid in production.

```go
sentrytrace.WithSentry(tracing.WithContentCapture())
```

## See Also

- [OpenTelemetry Tracing](tracing.md) — base tracing setup and span hierarchy
- [Prometheus Metrics](metrics.md) — counters and histograms
