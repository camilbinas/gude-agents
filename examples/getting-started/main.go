// Run:
//
//	go run ./getting-started

package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Cheapest())
	a, err := agent.New(
		provider,
		"You are a helpful assistant. Be concise.",
		agent.WithName("helpful-assistant"),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := a.Invoke(agent.Background(), "What is the capital of France?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
