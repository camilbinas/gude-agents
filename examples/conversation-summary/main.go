// Run:
//
//	go run ./conversation-summary

package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())
	ctx := agent.Background().WithConversationID("summary-demo")

	// Threshold of 10 turns — summarization triggers at 80% (16 messages).
	store := conversation.NewInMemory()
	summarized, err := conversation.NewSummary(
		store, 10, conversation.DefaultSummaryFunc(provider),
		conversation.WithSummaryLogger(log.Default()),
		conversation.WithPreserveRecentMessages(1),
	)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.New(
		provider,
		"You are a helpful assistant. Be concise.",
		agent.WithConversationStore(summarized),
		agent.WithSyncConversation(),
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
		result, err := a.Invoke(ctx, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Turn %d: %s\n", i+1, result.Text)
	}

	// WithSyncConversation makes each invocation wait for summarization.
	result, err := a.Invoke(ctx, "Remind me what city I live in.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Turn %d: %s\n", len(questions)+1, result.Text)

	snapshot, err := store.Load(ctx, "summary-demo")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nMessages in store after Turn %d: %d\n", len(questions)+1, len(snapshot.Messages))
	for i, m := range snapshot.Messages {
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
