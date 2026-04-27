// Run:
//
//	go run ./conversation-strategies

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())

	// In-memory store as the base layer.
	store := conversation.NewInMemory()

	// Compose strategies: Filter strips tool blocks, Window keeps last 20 messages.
	windowed := conversation.NewWindow(store, 20)
	filtered := conversation.NewFilter(windowed)

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithConversation(filtered, "demo-conversation"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// Multi-turn conversation through the composed pipeline.
	result, _, err := a.Invoke(ctx, "My name is Alice. Remember that.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Turn 1:", result)

	result, _, err = a.Invoke(ctx, "What is my name?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Turn 2:", result)

	ids, _ := store.List(ctx)
	fmt.Printf("\nConversations in store: %v\n", ids)

	_ = store.Delete(ctx, "demo-conversation")
	ids, _ = store.List(ctx)
	fmt.Printf("After delete: %v\n", ids)
}
