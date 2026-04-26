// Example: Two independent conversations sharing a single agent instance.
//
// A single Agent is created with WithSharedMemory — no default conversation ID.
// Each conversation supplies its own ID per invocation via WithConversationID,
// so their histories are stored and retrieved independently.
//
// Key concepts demonstrated:
//   - agent.WithSharedConversation — shared store, no default conversation ID
//   - agent.WithConversationID     — per-invocation conversation scoping
//   - conversation.NewStore        — in-memory conversation store
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
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())

	store := conversation.NewInMemory()

	// WithSharedConversation: no default conversation ID — each call must supply one.
	a, err := agent.Default(
		provider,
		prompt.Text("You are a friendly assistant. Remember details the user shares."),
		nil,
		agent.WithSharedConversation(store),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Conversation A — Alice
	ctxA := agent.WithConversationID(context.Background(), "conv-alice")
	ctxB := agent.WithConversationID(context.Background(), "conv-bob")

	invoke := func(ctx context.Context, label, msg string) string {
		result, _, err := a.Invoke(ctx, msg)
		if err != nil {
			log.Fatalf("%s: %v", label, err)
		}
		fmt.Printf("[%s] User: %s\n[%s]  Bot: %s\n\n", label, msg, label, result)
		return result
	}

	// Alice's conversation
	invoke(ctxA, "Alice", "Hi, my name is Alice and I work in travel tech.")
	invoke(ctxA, "Alice", "What field do I work in?")

	// Bob's conversation — completely separate history
	invoke(ctxB, "Bob", "Hey, I'm Bob and I'm a software engineer.")
	invoke(ctxB, "Bob", "What's my profession?")

	// Back to Alice — she still remembers her own context
	invoke(ctxA, "Alice", "Do you remember my name?")

	// Bob doesn't know anything about Alice
	invoke(ctxB, "Bob", "Do you know someone named Alice?")

	// Show what's in the store
	ids, _ := store.List(context.Background())
	fmt.Printf("Conversations in store: %v\n", ids)
}
