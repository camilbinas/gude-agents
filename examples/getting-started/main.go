// Run:
//
//	go run ./simple

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Cheapest())

	a, err := agent.Default(provider, prompt.Text("You are a helpful assistant. Be concise."), nil)
	if err != nil {
		log.Fatal(err)
	}

	result, usage, err := a.Invoke(context.Background(), "What is the capital of France?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result)
	fmt.Printf("Tokens: %d in, %d out\n", usage.InputTokens, usage.OutputTokens)
}
