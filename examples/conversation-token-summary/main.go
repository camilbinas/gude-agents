// Run:
//
//	go run ./conversation-token-summary

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

	ctx := context.Background()

	// Token threshold of 600 — summarization triggers at 80% (480 input tokens).
	// This triggers after roughly 7-8 exchanges as the conversation context grows.
	store := conversation.NewInMemory()
	summarized, err := conversation.NewTokenSummary(
		store, 600, conversation.DefaultSummaryFunc(provider),
		conversation.WithTokenSummaryLogger(log.Default()),
		conversation.WithTokenPreserveRecentMessages(2),
		conversation.WithTokenTriggerThreshold(80),
	)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithConversation(summarized, "token-summary-demo"),
		agent.WithSynchronousConversation(),
	)
	if err != nil {
		log.Fatal(err)
	}

	questions := []string{
		"My name is Alice and I live in Tokyo.",
		"I work as a data scientist at a large bank.",
		"My favourite language is Python but I also use Go.",
		"I have two cats named Mochi and Sushi.",
		"I enjoy running marathons — I've done 5 so far.",
		"My favourite book is Designing Data-Intensive Applications.",
		"I prefer working from coffee shops.",
		"What do you know about me so far?",
	}

	for i, q := range questions {
		result, usage, err := a.Invoke(ctx, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Turn %d [%d input tokens]: %s\n", i+1, usage.InputTokens, result)
	}

	// Final check — the agent should still know everything despite summarization.
	result, usage, err := a.Invoke(ctx, "What are my cats' names?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Turn %d [%d input tokens]: %s\n", len(questions)+1, usage.InputTokens, result)

	// Inspect the store.
	msgs, _ := store.Load(ctx, "token-summary-demo")
	fmt.Printf("\nMessages in store after Turn %d: %d\n", len(questions)+1, len(msgs))
	for i, m := range msgs {
		for _, b := range m.Content {
			if tb, ok := b.(agent.TextBlock); ok {
				preview := tb.Text
				if len(preview) > 100 {
					preview = preview[:100] + "..."
				}
				fmt.Printf("  [%d] %s: %s\n", i, m.Role, preview)
			}
		}
	}
}
