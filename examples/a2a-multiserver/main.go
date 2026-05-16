// Example: Host multiple agents on a single HTTP server with path-prefix routing.
//
// Each agent gets its own Agent Card and JSON-RPC endpoint under a unique prefix.
// Clients discover agents at {prefix}/.well-known/agent-card.json.
//
// Test with curl:
//
//	# Discover the summarizer agent
//	curl http://localhost:8080/agents/summarizer/.well-known/agent-card.json | jq
//
//	# Discover the translator agent
//	curl http://localhost:8080/agents/translator/.well-known/agent-card.json | jq
//
//	# Send a message to the summarizer
//	curl -X POST http://localhost:8080/agents/summarizer/ \
//	  -H "Content-Type: application/json" \
//	  -d '{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"role":"user","parts":[{"kind":"text","text":"Summarize: Go is a statically typed, compiled language designed at Google."}]}}}'
//
// Run:
//
//	go run ./a2a-multiserver

package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/a2a"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())

	// Create two agents with different capabilities.
	summarizer, err := agent.Default(
		provider,
		prompt.Text("You are a summarization expert. Produce concise summaries."),
		nil,
		agent.WithName("summarizer"),
	)
	if err != nil {
		log.Fatal(err)
	}

	translator, err := agent.Default(
		provider,
		prompt.Text("You are a translator. Translate text to the requested language."),
		nil,
		agent.WithName("translator"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Host both agents on a single server with path-prefix routing.
	ms, err := a2a.NewMultiServer([]a2a.AgentRegistration{
		{Prefix: "/agents/summarizer", Agent: summarizer},
		{Prefix: "/agents/translator", Agent: translator},
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Println("A2A MultiServer listening on :8080")
	fmt.Println("  Summarizer: http://localhost:8080/agents/summarizer/.well-known/agent-card.json")
	fmt.Println("  Translator: http://localhost:8080/agents/translator/.well-known/agent-card.json")
	if err := ms.ListenAndServe(ctx, ":8080"); err != nil {
		log.Fatal(err)
	}
}
