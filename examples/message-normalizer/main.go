// Example: Message normalizer strategies.
//
// Shows the three normalization strategies (Merge, Fill, Remove) that repair
// invalid message sequences before they reach LLM providers.
//
// Run:
//
//	go run ./message-normalizer

package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Cheapest())

	// ---------------------------------------------------------------
	// 1. Standalone usage — normalize a message slice directly.
	// ---------------------------------------------------------------
	fmt.Println("=== Standalone NormalizeMessages ===")

	broken := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Here's what I found."}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Let me elaborate."}}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Thanks!"}}},
	}

	fmt.Println("\nBefore normalization:")
	printMessages(broken)

	// Merge: combines the two assistant messages into one.
	merged := agent.NormalizeMessages(broken, agent.NormMerge)
	fmt.Println("\nAfter Merge:")
	printMessages(merged)

	// Fill: inserts synthetic messages to restore alternation.
	filled := agent.NormalizeMessages(broken, agent.NormFill)
	fmt.Println("\nAfter Fill:")
	printMessages(filled)

	// Remove: keeps only the last assistant message in the run.
	removed := agent.NormalizeMessages(broken, agent.NormRemove)
	fmt.Println("\nAfter Remove:")
	printMessages(removed)

	// ---------------------------------------------------------------
	// 2. Agent integration — normalizer runs automatically.
	// ---------------------------------------------------------------
	fmt.Println("\n=== Agent with Fill strategy ===")

	a, err := agent.Default(provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithMessageNormalizer(agent.NormFill),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "What is 2 + 2?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Response:", result)

	// ---------------------------------------------------------------
	// 3. Disable normalization entirely.
	// ---------------------------------------------------------------
	fmt.Println("\n=== Agent with normalization disabled ===")

	a2, err := agent.Default(provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithoutMessageNormalizer(),
	)
	if err != nil {
		log.Fatal(err)
	}

	result2, _, err := a2.Invoke(context.Background(), "What is 3 + 3?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Response:", result2)
}

func printMessages(msgs []agent.Message) {
	for i, m := range msgs {
		var parts []string
		for _, b := range m.Content {
			if tb, ok := b.(agent.TextBlock); ok {
				parts = append(parts, tb.Text)
			}
		}
		fmt.Printf("  [%d] %s: %s\n", i, m.Role, strings.Join(parts, " | "))
	}
}
