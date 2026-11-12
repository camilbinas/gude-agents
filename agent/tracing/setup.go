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
	// insecure, when non-nil, forces the transport security explicitly and
	// overrides the environment. nil means "decide from endpoint + env".
	insecure *bool
}

// WithInsecure forces plaintext (insecure bool = true) or TLS (false) for the
// OTLP gRPC export, overriding the OTEL_EXPORTER_OTLP_INSECURE /
// OTEL_EXPORTER_OTLP_TRACES_INSECURE environment variables.
func WithInsecure(insecure bool) SetupOption {
	return func(c *setupConfig) { c.insecure = &insecure }
}

// Setup configures a TracerProvider with a batch SpanProcessor and OTLP gRPC exporter.
// It reads OTEL_EXPORTER_OTLP_ENDPOINT from the environment, defaulting to localhost:4317.
// The provider is set as the global OTEL TracerProvider.
// Returns a shutdown function that flushes pending spans and shuts down the provider.
//
// Transport security: by default the export is insecure (plaintext), matching
// the localhost default endpoint. This default is overridden, in order of
// precedence, by the WithInsecure option and then by the standard
// OTEL_EXPORTER_OTLP_TRACES_INSECURE / OTEL_EXPORTER_OTLP_INSECURE environment
// variables (set either to "false" to enable TLS when pointing at a remote
// collector). Previously the export was always plaintext regardless of these
// signals, which could silently ship spans to a remote endpoint unencrypted.
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

// insecureExport decides whether the OTLP gRPC export should use an insecure
// (plaintext) connection. Precedence: explicit WithInsecure option, then the
// standard OTEL_EXPORTER_OTLP_TRACES_INSECURE / OTEL_EXPORTER_OTLP_INSECURE
// environment variables, then the historical default of insecure=true.
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
	return true // historical default: plaintext (suits the localhost default endpoint)
}

// TracingPreset returns an agent.Option that enables tracing using the global
// TracerProvider (typically set by Setup). Suitable for use in agent preset constructors.
func TracingPreset() agent.Option {
	return WithTracing(nil)
}
