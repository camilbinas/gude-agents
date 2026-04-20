// Example: OpenTelemetry metrics for agent invocations.
//
// Demonstrates how to enable OTEL metrics on an agent so that every
// invocation, iteration, provider call, and tool execution is recorded as
// counters and histograms via the OpenTelemetry metrics SDK.
//
// The example auto-detects whether an OTLP collector is running:
//
//   - If a collector is reachable on localhost:4317, metrics are exported
//     via OTLP gRPC. This is the path for production setups where you
//     already have a collector forwarding to Prometheus, Datadog, etc.
//   - If no collector is found, metrics are printed to stdout every 10s
//     so you can see them without any infrastructure.
//
// To run (no extra setup needed):
//
//	go run ./metrics-otel
//
// Set OTEL_EXPORTER_OTLP_ENDPOINT to point at an existing collector.

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
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"

	otelmetrics "github.com/camilbinas/gude-agents/agent/metrics/otel"
)

func main() {
	ctx := context.Background()

	// 1. Set up the OTEL MeterProvider — tries OTLP first, falls back to stdout.
	mp, shutdown, err := setupMeterProvider(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Printf("meter provider shutdown: %v", err)
		}
	}()

	// 2. Create a provider.
	provider := bedrock.Must(bedrock.Standard())

	// 3. Create the agent with OTEL metrics and a couple of tools.
	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant with access to weather and time tools. Be concise."),
		[]tool.Tool{utils.WeatherTool(), utils.TimeTool()},
		otelmetrics.WithMetrics(mp, otelmetrics.WithNamespace("gude")),
		agent.WithName("metrics-demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Interactive chat loop — metrics accumulate across invocations.
	fmt.Println("OTEL metrics agent ready. Type 'quit' to exit.")
	fmt.Println("Metrics are exported via OTLP or printed to stdout every 10s.")
	fmt.Println()
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()

	utils.Chat(ctx, a, utils.ChatOptions{ShowUsage: true})
	fmt.Println("Flushing metrics...")
}

// setupMeterProvider configures an OTEL MeterProvider. It probes the OTLP
// endpoint first; if reachable, metrics go to the collector. Otherwise it
// falls back to stdout.
func setupMeterProvider(ctx context.Context) (*sdkmetric.MeterProvider, func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("gude-agents-metrics-example"),
		),
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
