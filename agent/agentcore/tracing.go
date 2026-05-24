package agentcore

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tracing"
)

// TracingShutdown flushes and stops the tracing pipeline created by
// SetupTracing. Always call it during shutdown so buffered spans are
// exported before the process exits.
type TracingShutdown func(context.Context) error

// SetupTracingOption configures SetupTracing.
type SetupTracingOption func(*setupTracingConfig)

type setupTracingConfig struct {
	endpoint       string
	useHTTP        bool
	insecure       bool
	captureContent bool
}

// WithOTLPEndpoint overrides the OTLP collector endpoint. When unset, the
// helper reads OTEL_EXPORTER_OTLP_ENDPOINT and finally falls back to
// localhost:4317 for gRPC or http://localhost:4318 for HTTP.
//
// AgentCore's managed observability runs an ADOT collector inside the
// runtime container that listens on the standard OTLP ports, so the default
// usually works without configuration when deployed.
func WithOTLPEndpoint(endpoint string) SetupTracingOption {
	return func(c *setupTracingConfig) {
		c.endpoint = endpoint
	}
}

// WithOTLPHTTP uses the HTTP OTLP exporter instead of gRPC. Useful when the
// collector only exposes HTTP/proto on port 4318.
func WithOTLPHTTP() SetupTracingOption {
	return func(c *setupTracingConfig) {
		c.useHTTP = true
	}
}

// WithOTLPInsecure disables TLS for the OTLP exporter. Required for the
// in-container ADOT sidecar.
func WithOTLPInsecure() SetupTracingOption {
	return func(c *setupTracingConfig) {
		c.insecure = true
	}
}

// WithTracingContentCapture enables capture of prompts, completions, and
// tool I/O on spans. Off by default to keep payloads clear of sensitive
// content. Mirrors tracing.WithContentCapture().
func WithTracingContentCapture() SetupTracingOption {
	return func(c *setupTracingConfig) {
		c.captureContent = true
	}
}

// SetupTracing wires an OpenTelemetry tracer provider configured for AWS
// Bedrock AgentCore observability (service.name = {RuntimeName}.DEFAULT) and
// returns an agent.Option that registers the AgentCore attribute scheme.
// The returned shutdown function flushes pending spans on process exit.
func SetupTracing(ctx context.Context, runtimeName string, opts ...SetupTracingOption) (TracingShutdown, agent.Option, error) {
	if runtimeName == "" {
		return nil, nil, fmt.Errorf("agentcore tracing: runtimeName is required")
	}

	cfg := setupTracingConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	res, err := buildAgentCoreResource(runtimeName)
	if err != nil {
		return nil, nil, fmt.Errorf("agentcore tracing: building resource: %w", err)
	}

	tp, shutdown, err := buildTracerProvider(ctx, cfg, res)
	if err != nil {
		return nil, nil, fmt.Errorf("agentcore tracing: building tracer provider: %w", err)
	}
	otel.SetTracerProvider(tp)

	tracingOpts := []tracing.TracingOption{tracing.WithScheme(tracing.AgentCoreScheme())}
	if cfg.captureContent {
		tracingOpts = append(tracingOpts, tracing.WithContentCapture())
	}

	return shutdown, tracing.WithTracing(tp, tracingOpts...), nil
}

// buildAgentCoreResource constructs the OTel Resource that the AgentCore
// Recommendations API expects (service.name = {RuntimeName}.DEFAULT). It
// merges with the SDK's default resource so values from
// OTEL_RESOURCE_ATTRIBUTES and platform detectors (host name, process pid)
// are preserved.
func buildAgentCoreResource(runtimeName string) (*resource.Resource, error) {
	serviceName := runtimeName + ".DEFAULT"
	return resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName)),
	)
}

func buildTracerProvider(ctx context.Context, cfg setupTracingConfig, res *resource.Resource) (*sdktrace.TracerProvider, TracingShutdown, error) {
	endpoint := cfg.endpoint
	if endpoint == "" {
		endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}

	var (
		tp       *sdktrace.TracerProvider
		shutdown TracingShutdown
	)

	switch {
	case cfg.useHTTP:
		exp, err := newHTTPExporter(ctx, endpoint, cfg.insecure)
		if err != nil {
			return nil, nil, err
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
		)
	default:
		exp, err := newGRPCExporter(ctx, endpoint, cfg.insecure)
		if err != nil {
			return nil, nil, err
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
		)
	}

	shutdown = func(ctx context.Context) error {
		return tp.Shutdown(ctx)
	}
	return tp, shutdown, nil
}

func newGRPCExporter(ctx context.Context, endpoint string, insecure bool) (sdktrace.SpanExporter, error) {
	opts := []otlptracegrpc.Option{}
	if endpoint != "" {
		opts = append(opts, otlptracegrpc.WithEndpoint(stripScheme(endpoint)))
	}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	return otlptracegrpc.New(ctx, opts...)
}

func newHTTPExporter(ctx context.Context, endpoint string, insecure bool) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{}
	if endpoint != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(stripScheme(endpoint)))
	}
	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	return otlptracehttp.New(ctx, opts...)
}

// stripScheme returns the host[:port] portion of an OTLP endpoint, stripping
// any http:// or https:// prefix. The OTel SDK exporters expect a bare
// host:port and reject URLs.
func stripScheme(endpoint string) string {
	if i := strings.Index(endpoint, "://"); i >= 0 {
		return endpoint[i+3:]
	}
	return endpoint
}
