// Run:
//
//	go run ./getting-started

package main

import (
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Cheapest())

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithName("helpful-assistant"),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	if _, err = a.Invoke(agent.Background(), "What is the capital of France?"); err != nil {
		log.Fatal(err)
	}
}
