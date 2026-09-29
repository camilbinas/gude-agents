// Example: OpenTelemetry tracing for agent invocations.
//
// The example exports spans to an OTLP collector when reachable and prints a
// formatted tree after each invocation otherwise.
//
// To run:
//
//	go run ./tracing-otel

package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tracing"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := agent.Background()
	treeExp, shutdown, err := setupTracing(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Printf("tracing shutdown: %v", err)
		}
	}()

	type weatherInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}
	weatherTool := tool.New("get_weather", "Get the current weather for a city", func(_ context.Context, input weatherInput) (string, error) {
		temp := 15 + rand.IntN(20)
		return fmt.Sprintf(`{"city": %q, "temp_c": %d, "condition": "partly cloudy"}`, input.City, temp), nil
	})

	type timeInput struct {
		Timezone string `json:"timezone" description:"IANA timezone name (e.g. America/New_York)" required:"true"`
	}
	timeTool := tool.New("get_time", "Get the current time in a timezone", func(_ context.Context, input timeInput) (string, error) {
		return fmt.Sprintf(`{"timezone": %q, "time": "14:32"}`, input.Timezone), nil
	})

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a helpful assistant with access to weather and time tools. Be concise.",
		agent.WithTools(weatherTool, timeTool),
		tracing.WithTracing(nil),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Traced agent ready. Type 'quit' to exit.")
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()

	utils.Chat(ctx, a, utils.ChatOptions{
		AfterInvoke: func(_ *agent.Context, _ agent.Result, _ error) {
			if treeExp != nil {
				treeExp.Flush()
			}
		},
	})
	fmt.Println("Flushing traces...")
}

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
