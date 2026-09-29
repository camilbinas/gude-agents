// Example: OpenTelemetry metrics for agent invocations.
//
// The example auto-detects whether an OTLP collector is running. Metrics are
// exported over OTLP when reachable and printed to stdout otherwise.
//
// To run:
//
//	go run ./metrics-otel

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/camilbinas/gude-agents/agent"
	otelmetrics "github.com/camilbinas/gude-agents/agent/metrics/otel"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := agent.Background()
	mp, shutdown, err := setupMeterProvider(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Printf("meter provider shutdown: %v", err)
		}
	}()

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a helpful assistant with access to weather and time tools. Be concise.",
		agent.WithTools(utils.WeatherTool(), utils.TimeTool()),
		otelmetrics.WithMetrics(mp, otelmetrics.WithNamespace("gude")),
		agent.WithName("metrics-demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("OTEL metrics agent ready. Type 'quit' to exit.")
	fmt.Println("Metrics are exported via OTLP or printed to stdout every 10s.")
	fmt.Println()
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()
	utils.Chat(ctx, a)
	fmt.Println("Flushing metrics...")
}

func setupMeterProvider(ctx context.Context) (*sdkmetric.MeterProvider, func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("gude-agents-metrics-example")),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("resource: %w", err)
	}

	if isReachable(endpoint) {
		log.Printf("OTLP collector reachable at %s — exporting metrics via gRPC", endpoint)
		exp, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(endpoint),
			otlpmetricgrpc.WithInsecure(),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("otlp metric exporter: %w", err)
		}
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(10*time.Second))),
			sdkmetric.WithResource(res),
		)
		return mp, mp.Shutdown, nil
	}

	log.Printf("No OTLP collector at %s — using stdout metric exporter", endpoint)
	exp, err := stdoutmetric.New()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(10*time.Second))),
		sdkmetric.WithResource(res),
	)
	return mp, mp.Shutdown, nil
}

func isReachable(endpoint string) bool {
	conn, err := net.DialTimeout("tcp", endpoint, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
