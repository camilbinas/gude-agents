// Run:
//
//	go run ./conversational

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	store := memory.NewStore()

	a, err := agent.Default(
		provider,
		prompt.Text("You are a friendly assistant. Remember details the user shares."),
		nil,
		agent.WithMemory(store, "chat-session-1"),
		agent.WithMaxIterations(10),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	turns := []string{
		"Hi, my name is Alice and I love hiking.",
		"What's a good trail for beginners?",
		"What's my name and what do I enjoy?",
	}

	for i, msg := range turns {
		result, _, err := a.Invoke(ctx, msg)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Turn %d: %s\n\n", i+1, result)
	}
}
