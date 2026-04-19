package tracing

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	agent "github.com/camilbinas/gude-agents/agent"
)

// Setup configures a TracerProvider with a batch SpanProcessor and OTLP gRPC exporter.
// It reads OTEL_EXPORTER_OTLP_ENDPOINT from the environment, defaulting to localhost:4317.
// The provider is set as the global OTEL TracerProvider.
// Returns a shutdown function that flushes pending spans and shuts down the provider.
func Setup(ctx context.Context) (shutdown func(context.Context) error, err error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
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

// TracingPreset returns an agent.Option that enables tracing using the global
// TracerProvider (typically set by Setup). Suitable for use in agent preset constructors.
func TracingPreset() agent.Option {
	return WithTracing(nil)
}
