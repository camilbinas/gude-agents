// Example: Expose a gude-agents Agent over the A2A protocol.
//
// Starts an HTTP server that speaks the Google A2A protocol (JSON-RPC + SSE).
// Any A2A-compliant client can discover the agent via /.well-known/agent-card.json
// and interact with it via message/send.
//
// Test with curl:
//
//	# Discover the agent
//	curl http://localhost:8080/.well-known/agent-card.json | jq
//
//	# Send a message (JSON-RPC)
//	curl -X POST http://localhost:8080/ \
//	  -H "Content-Type: application/json" \
//	  -d '{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"What is the capital of France?"}]}}}'
//
// Run:
//
//	go run ./a2a-server

package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/a2a"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())

	type weatherInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}
	weather := tool.New(
		"get_weather",
		"Get current weather for a city",
		func(_ context.Context, in weatherInput) (string, error) {
			return fmt.Sprintf("Weather in %s: 22°C, partly cloudy", in.City), nil
		},
	)

	a, err := agent.New(
		provider,
		"You are a helpful travel assistant. Provide concise, accurate travel advice.",
		agent.WithTools(weather),
		agent.WithName("travel-assistant"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Create an A2A server wrapping the agent.
	srv, err := a2a.NewServer(a,
		[]a2a.CardOption{
			a2a.WithCardURL("http://localhost:8080"),
			a2a.WithCardVersion("1.0.0"),
			a2a.WithCardDescription("A travel assistant that provides weather and destination info"),
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	// Start with graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Println("A2A server listening on :8080")
	fmt.Println("Agent card: http://localhost:8080/.well-known/agent-card.json")
	if err := srv.ListenAndServe(ctx, ":8080"); err != nil {
		log.Fatal(err)
	}
}
