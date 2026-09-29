// Example: Prometheus metrics for agent invocations.
//
// The example exposes metrics on :2112/metrics while running an interactive
// chat loop.
//
// To run:
//
//	go run ./metrics-prometheus

package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/camilbinas/gude-agents/agent"
	prometheus "github.com/camilbinas/gude-agents/agent/metrics/prometheus"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := agent.Background()
	metricsOpt, metricsHandler := prometheus.NewHandler(
		prometheus.WithNamespace("gude"),
	)

	http.Handle("/metrics", metricsHandler)
	go func() {
		if err := http.ListenAndServe(":2112", nil); err != nil {
			log.Printf("metrics server: %v", err)
		}
	}()

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a helpful assistant with access to weather and time tools. Be concise.",
		agent.WithTools(utils.WeatherTool(), utils.TimeTool()),
		metricsOpt,
		agent.WithName("metrics-demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Metrics agent ready. Type 'quit' to exit.")
	fmt.Println("Prometheus metrics available at http://localhost:2112/metrics")
	fmt.Println()
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()
	utils.Chat(ctx, a)
}
