// Package sentry provides Sentry integration for gude-agents tracing.
//
// It wraps the core OTEL tracing module and adds:
//   - Sentry SDK initialization for error capture as Issues
//   - OTLP trace export to Sentry's ingestion endpoint (derived from DSN)
//   - Error capture middleware that classifies agent errors and links them to traces
//   - Breadcrumb middleware that records tool calls in the Sentry breadcrumb trail
//
// Quick start:
//
//	import sentrytrace "github.com/camilbinas/gude-agents/agent/tracing/sentry"
//
//	shutdown, err := sentrytrace.Setup(ctx, sentrytrace.Config{
//	    DSN: "https://key@o123.ingest.us.sentry.io/456",
//	})
//	defer shutdown(ctx)
//
//	a, err := agent.New(provider, instructions, tools,
//	    sentrytrace.WithSentry(),
//	)
package sentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	gosentry "github.com/getsentry/sentry-go"
	sentryotel "github.com/getsentry/sentry-go/otel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tracing"
)

// ---------------------------------------------------------------------------
// Config and Setup
// ---------------------------------------------------------------------------

// Config holds the configuration for Sentry integration.
type Config struct {
	// DSN is the Sentry project DSN.
	// Required. Find it in Project Settings > Client Keys (DSN).
	// Example: https://abc123@o123.ingest.us.sentry.io/456
	//
	// The OTLP endpoint and public key are derived from the DSN automatically.
	DSN string

	// Environment is the Sentry environment tag (e.g. "production", "staging").
	// Defaults to "development".
	Environment string

	// Release is the Sentry release tag (e.g. "myapp@1.0.0").
	// Optional.
	Release string

	// ServiceName is the OTEL service name resource attribute.
	// Defaults to "gude-agents".
	ServiceName string

	// TracesSampleRate controls what fraction of traces are sent (0.0 to 1.0).
	// Defaults to 1.0 (all traces).
	TracesSampleRate float64

	// Debug enables Sentry SDK debug logging.
	Debug bool
}

// Setup initializes the Sentry SDK and configures an OTLP trace exporter
// pointed at Sentry's ingestion endpoint. The OTLP endpoint and auth key
// are derived from the DSN automatically.
//
// It sets the global OTEL TracerProvider.
// Returns a shutdown function that flushes pending spans and Sentry events.
func Setup(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("sentry: DSN is required")
	}

	parsed, err := parseDSN(cfg.DSN)
	if err != nil {
		return nil, err
	}

	env := cfg.Environment
	if env == "" {
		env = "development"
	}
	sampleRate := cfg.TracesSampleRate
	if sampleRate == 0 {
		sampleRate = 1.0
	}
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "gude-agents"
	}

	// Initialize Sentry SDK for error capture, with OTEL integration
	// so that captured errors are linked to the active OTEL trace.
	if err := gosentry.Init(gosentry.ClientOptions{
		Dsn:              cfg.DSN,
		EnableTracing:    true,
		TracesSampleRate: sampleRate,
		Environment:      env,
		Release:          cfg.Release,
		Debug:            cfg.Debug,
		Integrations: func(integrations []gosentry.Integration) []gosentry.Integration {
			return append(integrations, sentryotel.NewOtelIntegration())
		},
	}); err != nil {
		return nil, fmt.Errorf("sentry.Init: %w", err)
	}

	// Set up OTLP HTTP exporter → Sentry's OTLP endpoint.
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(parsed.otlpEndpoint),
		otlptracehttp.WithHeaders(map[string]string{
			"x-sentry-auth": fmt.Sprintf("sentry sentry_key=%s", parsed.publicKey),
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("sentry otlp exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
		)),
	)
	otel.SetTracerProvider(tp)

	return func(ctx context.Context) error {
		gosentry.Flush(5 * time.Second)
		return tp.Shutdown(ctx)
	}, nil
}

// ---------------------------------------------------------------------------
// Agent Option
// ---------------------------------------------------------------------------

// WithSentry returns an agent.Option that enables OTEL tracing (using the
// global TracerProvider set by Setup).
//
// Options:
//   - Pass tracing.WithContentCapture() to include prompts/responses in spans.
func WithSentry(opts ...tracing.TracingOption) agent.Option {
	return func(a *agent.Agent) error {
		return tracing.WithTracing(nil, opts...)(a)
	}
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// ErrorCaptureMiddleware returns an agent.Middleware that captures tool errors
// as Sentry issues linked to the active OTEL trace.
func ErrorCaptureMiddleware() agent.Middleware {
	return func(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
		return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
			result, err := next(ctx, toolName, input)
			if err != nil {
				gosentry.WithScope(func(scope *gosentry.Scope) {
					scope.SetTag("agent.error_type", "tool_error")
					scope.SetTag("tool.name", toolName)
					setTraceContext(ctx, scope)
					client := gosentry.CurrentHub().Client()
					if client != nil {
						client.CaptureException(err, &gosentry.EventHint{Context: ctx}, scope)
					}
				})
			}
			return result, err
		}
	}
}

// BreadcrumbMiddleware returns an agent.Middleware that adds a Sentry
// breadcrumb for every tool call.
func BreadcrumbMiddleware() agent.Middleware {
	return func(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
		return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
			start := time.Now()
			result, err := next(ctx, toolName, input)
			elapsed := time.Since(start)

			data := map[string]any{
				"tool":     toolName,
				"duration": elapsed.String(),
			}

			inputStr := string(input)
			if len(inputStr) > 200 {
				inputStr = inputStr[:200] + "..."
			}
			data["input"] = inputStr

			level := gosentry.LevelInfo
			if err != nil {
				level = gosentry.LevelError
				data["error"] = err.Error()
			} else {
				preview := result
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				data["result"] = preview
			}

			gosentry.AddBreadcrumb(&gosentry.Breadcrumb{
				Category:  "agent.tool",
				Message:   fmt.Sprintf("tool.%s", toolName),
				Level:     level,
				Data:      data,
				Timestamp: time.Now(),
			})

			return result, err
		}
	}
}

// ---------------------------------------------------------------------------
// Error Capture
// ---------------------------------------------------------------------------

// CaptureAgentErrorOpts holds optional context for CaptureAgentError.
type CaptureAgentErrorOpts struct {
	// ModelID is the LLM model identifier.
	ModelID string
}

// CaptureAgentError sends an agent error to Sentry as an Issue, enriched with
// error classification, the user's message, token usage, and optional model info.
func CaptureAgentError(ctx context.Context, err error, userMessage string, usage agent.TokenUsage, opts ...CaptureAgentErrorOpts) {
	gosentry.WithScope(func(scope *gosentry.Scope) {
		scope.SetTag("agent.error_type", classifyError(err))
		scope.SetExtra("user_message", userMessage)
		scope.SetExtra("token_usage.input", usage.InputTokens)
		scope.SetExtra("token_usage.output", usage.OutputTokens)
		scope.SetExtra("token_usage.total", usage.Total())

		if len(opts) > 0 && opts[0].ModelID != "" {
			scope.SetTag("agent.model_id", opts[0].ModelID)
		}

		setTraceContext(ctx, scope)

		// Pass context in EventHint so the Sentry OTEL integration can
		// link this error to the active OTEL trace in the Sentry UI.
		client, s := gosentry.CurrentHub().Client(), scope
		if client != nil {
			client.CaptureException(err, &gosentry.EventHint{Context: ctx}, s)
		}
	})
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func setTraceContext(ctx context.Context, scope *gosentry.Scope) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		scope.SetTag("trace_id", span.SpanContext().TraceID().String())
		scope.SetTag("span_id", span.SpanContext().SpanID().String())
	}
}

type parsedDSN struct {
	publicKey    string
	host         string
	projectID    string
	otlpEndpoint string
}

func parseDSN(dsn string) (*parsedDSN, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid DSN: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("invalid DSN: missing public key (expected https://<key>@<host>/<project_id>)")
	}
	publicKey := u.User.Username()
	host := u.Host

	path := strings.TrimSuffix(u.Path, "/")
	idx := strings.LastIndex(path, "/")
	var projectID string
	if idx >= 0 {
		projectID = path[idx+1:]
	} else {
		projectID = strings.TrimPrefix(path, "/")
	}
	if projectID == "" {
		return nil, fmt.Errorf("invalid DSN: missing project ID (expected https://<key>@<host>/<project_id>)")
	}

	otlpEndpoint := fmt.Sprintf("%s://%s/api/%s/integration/otlp/v1/traces", u.Scheme, host, projectID)

	return &parsedDSN{
		publicKey:    publicKey,
		host:         host,
		projectID:    projectID,
		otlpEndpoint: otlpEndpoint,
	}, nil
}

func classifyError(err error) string {
	var provErr *agent.ProviderError
	if errors.As(err, &provErr) {
		return "provider_error"
	}
	var toolErr *agent.ToolError
	if errors.As(err, &toolErr) {
		return "tool_error"
	}
	var guardErr *agent.GuardrailError
	if errors.As(err, &guardErr) {
		return "guardrail_error"
	}
	if errors.Is(err, agent.ErrTokenBudgetExceeded) {
		return "token_budget_exceeded"
	}
	if strings.Contains(err.Error(), "max iterations") {
		return "max_iterations_exceeded"
	}
	return "unknown"
}
