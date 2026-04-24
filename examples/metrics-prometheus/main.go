// Example: Prometheus metrics for agent invocations.
//
// Demonstrates how to enable Prometheus metrics on an agent so that every
// invocation, iteration, provider call, and tool execution is recorded as
// counters and histograms.
//
// The example starts an HTTP server on :2112/metrics and runs an interactive
// chat loop. While chatting, open another terminal and curl the metrics
// endpoint to see counters and histograms update in real time:
//
//	curl -s localhost:2112/metrics
//
// To run:
//
//	go run ./metrics

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"

	prometheus "github.com/camilbinas/gude-agents/agent/metrics/prometheus"
)

func main() {
	ctx := context.Background()

	// 1. Create the metrics option and HTTP handler together.
	//    NewHandler returns both so you can mount the handler on your
	//    HTTP server and pass the option to agent.New.
	metricsOpt, metricsHandler := prometheus.NewHandler(
		prometheus.WithNamespace("gude"),
	)

	// 2. Start the metrics HTTP server in the background.
	http.Handle("/metrics", metricsHandler)
	go func() {
		if err := http.ListenAndServe(":2112", nil); err != nil {
			log.Printf("metrics server: %v", err)
		}
	}()

	// 3. Create a provider.
	provider := bedrock.Must(bedrock.Standard())

	// 4. Create the agent with metrics and a couple of tools.
	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant with access to weather and time tools. Be concise."),
		[]tool.Tool{utils.WeatherTool(), utils.TimeTool()},
		metricsOpt,
		agent.WithName("metrics-demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 5. Interactive chat loop — metrics accumulate across invocations.
	fmt.Println("Metrics agent ready. Type 'quit' to exit.")
	fmt.Println("Prometheus metrics available at http://localhost:2112/metrics")
	fmt.Println()
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()

	utils.Chat(ctx, a)
}
