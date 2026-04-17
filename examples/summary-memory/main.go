// Run:
//
//	go run ./summary-memory

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

	ctx := context.Background()

	// Threshold of 10 turns — summarization triggers at 80% (16 messages).
	// WithPreserveRecentMessages(1) keeps the last turn out of the
	// SummaryFunc, so it always appears verbatim after the summary.
	store := memory.NewStore()
	summarized, err := memory.NewSummary(
		store, 10, memory.DefaultSummaryFunc(provider),
		memory.WithSummaryLogger(log.Default()),
		memory.WithPreserveRecentMessages(1),
	)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithMemory(summarized, "summary-demo"),
		agent.WithSynchronousMemory(),
	)
	if err != nil {
		log.Fatal(err)
	}

	questions := []string{
		"My name is Bob and I live in Berlin.",
		"I work as a software engineer at a startup.",
		"My favourite programming language is Go.",
		"I have been coding for 10 years.",
		"I enjoy hiking on weekends.",
		"My favourite book is The Pragmatic Programmer.",
		"I prefer working remotely.",
		"What do you know about me so far?",
	}

	for i, q := range questions {
		result, _, err := a.Invoke(ctx, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Turn %d: %s\n", i+1, result)
	}

	// With WithSynchronousMemory(), each Invoke blocks until background
	// summarization finishes before returning — no manual Wait() needed.
	// This demonstrates that summarization is transparent: the agent still
	// knows everything about Bob despite the condensed message count.
	result, _, err := a.Invoke(ctx, "Remind me what city I live in.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Turn %d: %s\n", len(questions)+1, result)

	// Inspect the store — should show the summarized + recent messages,
	// not the full original history.
	msgs, _ := store.Load(ctx, "summary-demo")
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
