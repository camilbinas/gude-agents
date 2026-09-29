// Example: Two independent conversations sharing a single agent instance.
//
// A single Agent is created with WithConversationStore. Each conversation
// supplies its own ID per invocation via WithConversationID, so their histories
// are stored and retrieved independently.
//
// Key concepts demonstrated:
//   - agent.WithConversationStore — one store shared by all invocations
//   - c.WithConversationID        — per-invocation conversation scoping
//   - conversation.NewInMemory    — in-memory conversation store
//
// Run:
//
//	go run ./conversation-shared

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())
	store := conversation.NewInMemory()

	a, err := agent.New(
		provider,
		"You are a friendly assistant. Remember details the user shares.",
		agent.WithConversationStore(store),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctxA := agent.Background().WithConversationID("conv-alice")
	ctxB := agent.Background().WithConversationID("conv-bob")

	invoke := func(c *agent.Context, label, msg string) string {
		result, err := a.Invoke(c, msg)
		if err != nil {
			log.Fatalf("%s: %v", label, err)
		}
		fmt.Printf("[%s] User: %s\n[%s]  Bot: %s\n\n", label, msg, label, result.Text)
		return result.Text
	}

	invoke(ctxA, "Alice", "Hi, my name is Alice and I work in travel tech.")
	invoke(ctxA, "Alice", "What field do I work in?")
	invoke(ctxB, "Bob", "Hey, I'm Bob and I'm a software engineer.")
	invoke(ctxB, "Bob", "What's my profession?")
	invoke(ctxA, "Alice", "Do you remember my name?")
	invoke(ctxB, "Bob", "Do you know someone named Alice?")

	ids, _ := store.List(context.Background())
	fmt.Printf("Conversations in store: %v\n", ids)
}
