// Example: OpenTelemetry tracing for agent invocations.
//
// Demonstrates how to enable distributed tracing on an agent so that every
// invocation, iteration, provider call, and tool execution produces OTEL
// spans.
//
// The example auto-detects whether an OTLP collector is running:
//
//   - If a collector is reachable (e.g. Jaeger on localhost:4317), spans are
//     exported via OTLP gRPC and you can view them in the collector UI.
//   - If no collector is found, spans are printed to stderr as a formatted
//     tree after each invocation completes.
//
// To run with Jaeger:
//
//	docker run -d --name jaeger \
//	  -p 16686:16686 \
//	  -p 4317:4317 \
//	  jaegertracing/all-in-one:latest
//	go run ./tracing
//	# open http://localhost:16686
//
// To run without a collector (console output):
//
//	go run ./tracing
//
// Set OTEL_EXPORTER_OTLP_ENDPOINT to override the default endpoint.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tracing"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := context.Background()

	// 1. Set up tracing — tries OTLP first, falls back to console tree.
	treeExp, shutdown, err := setupTracing(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Printf("tracing shutdown: %v", err)
		}
	}()

	// 2. Create a provider.
	provider := bedrock.Must(bedrock.Standard())

	// 3. Define some tools so we get tool spans in the trace.
	weatherTool := tool.NewRaw(
		"get_weather",
		"Get the current weather for a city",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "City name",
				},
			},
			"required": []any{"city"},
		},
		func(_ context.Context, input json.RawMessage) (string, error) {
			var req struct {
				City string `json:"city"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return "", err
			}
			temp := 15 + rand.IntN(20)
			return fmt.Sprintf(`{"city": %q, "temp_c": %d, "condition": "partly cloudy"}`, req.City, temp), nil
		},
	)

	timeTool := tool.NewString(
		"get_time",
		"Get the current time in a timezone",
		"timezone", "IANA timezone name (e.g. America/New_York)",
		func(_ context.Context, tz string) (string, error) {
			return fmt.Sprintf(`{"timezone": %q, "time": "14:32"}`, tz), nil
		},
	)

	// 4. Create the agent with tracing enabled.
	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant with access to weather and time tools. Be concise."),
		[]tool.Tool{weatherTool, timeTool},
		tracing.WithTracing(nil),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 5. Interactive loop — each invocation produces a full trace.
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Traced agent ready. Type 'quit' to exit.")
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()

	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "quit") {
			break
		}

		fmt.Print("Agent: ")
		usage, err := a.InvokeStream(ctx, input, func(chunk string) {
			fmt.Print(chunk)
		})
		fmt.Println()
		if err != nil {
			log.Printf("Error: %v", err)
		}
		fmt.Printf("  [tokens: %d in, %d out]\n", usage.InputTokens, usage.OutputTokens)

		// Flush the tree exporter after each invocation so the trace
		// prints immediately (only relevant for console mode).
		if treeExp != nil {
			treeExp.Flush()
		}
		fmt.Println()
	}

	fmt.Println("Flushing traces...")
}

// setupTracing configures a TracerProvider. It probes the OTLP endpoint first;
// if reachable, spans go to the collector (treeExp is nil). Otherwise it falls
// back to a tree formatter (treeExp is non-nil so the caller can Flush it).
func setupTracing(ctx context.Context) (treeExp *utils.TreeExporter, shutdown func(context.Context) error, err error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("gude-agents-example"),
	)

	if isReachable(endpoint) {
		log.Printf("OTLP collector reachable at %s — exporting spans via gRPC", endpoint)
		exp, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("otlp exporter: %w", err)
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		return nil, tp.Shutdown, nil
	}

	log.Printf("No OTLP collector at %s — using console tree formatter", endpoint)
	treeExp = utils.NewTreeExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(treeExp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return treeExp, tp.Shutdown, nil
}

func isReachable(endpoint string) bool {
	conn, err := net.DialTimeout("tcp", endpoint, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
