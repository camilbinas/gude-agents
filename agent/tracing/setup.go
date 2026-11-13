package tracing

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	agent "github.com/camilbinas/gude-agents/agent"
)

// SetupOption configures the Setup exporter.
type SetupOption func(*setupConfig)

type setupConfig struct {
	// insecure, when non-nil, overrides the environment. nil = decide from env.
	insecure *bool
}

// WithInsecure forces plaintext (true) or TLS (false) for the OTLP gRPC export,
// overriding the OTEL_EXPORTER_OTLP_[TRACES_]INSECURE environment variables.
func WithInsecure(insecure bool) SetupOption {
	return func(c *setupConfig) { c.insecure = &insecure }
}

// Setup configures a TracerProvider with a batch SpanProcessor and OTLP gRPC exporter.
// It reads OTEL_EXPORTER_OTLP_ENDPOINT from the environment, defaulting to localhost:4317.
// The provider is set as the global OTEL TracerProvider.
// Returns a shutdown function that flushes pending spans and shuts down the provider.
//
// The export defaults to insecure (plaintext); override via WithInsecure or the
// OTEL_EXPORTER_OTLP_[TRACES_]INSECURE env vars to enable TLS for a remote collector.
func Setup(ctx context.Context, opts ...SetupOption) (shutdown func(context.Context) error, err error) {
	var cfg setupConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	grpcOpts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
	if insecureExport(cfg) {
		grpcOpts = append(grpcOpts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("tracing setup: create exporter: %w", err)
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("gude-agents"),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// insecureExport reports whether the OTLP gRPC export uses plaintext.
// Precedence: WithInsecure option, then the *_INSECURE env vars, then true.
func insecureExport(cfg setupConfig) bool {
	if cfg.insecure != nil {
		return *cfg.insecure
	}
	for _, env := range []string{"OTEL_EXPORTER_OTLP_TRACES_INSECURE", "OTEL_EXPORTER_OTLP_INSECURE"} {
		if v, ok := os.LookupEnv(env); ok {
			if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
				return b
			}
		}
	}
	return true // default: plaintext
}

// TracingPreset returns an agent.Option that enables tracing using the global
// TracerProvider (typically set by Setup). Suitable for use in agent preset constructors.
func TracingPreset() agent.Option {
	return WithTracing(nil)
}
