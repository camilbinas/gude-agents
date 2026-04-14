package main

import (
	"context"
	"fmt"
	"log"
	"time"

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

	ctx := context.Background()

	// Threshold of 6 messages — summarization triggers at 80% (~5 messages).
	// WithPreserveRecentMessages(2) keeps the last 2 messages out of the
	// SummaryFunc, so they always appear verbatim after the summary.
	store := memory.NewStore()
	summarized := memory.NewSummary(
		store, 6, memory.DefaultSummaryFunc(provider),
		memory.WithSummaryLogger(log.Default()),
		memory.WithPreserveRecentMessages(2),
	)

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithMemory(summarized, "summary-demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	questions := []string{
		"My name is Bob and I live in Berlin.",
		"I work as a software engineer at a startup.",
		"What do you know about me so far?",
	}

	for i, q := range questions {
		result, _, err := a.Invoke(ctx, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Turn %d: %s\n", i+1, result)
	}

	// Give the background summarization goroutine time to finish.
	time.Sleep(3 * time.Second)

	// Inspect the store after summarization.
	msgs, _ := store.Load(ctx, "summary-demo")
	fmt.Printf("\nMessages in store after summarization: %d\n", len(msgs))
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
