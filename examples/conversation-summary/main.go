// Run: go run ./conversation-summary
package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/contextmanager"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())
	store := conversation.NewInMemory()
	manager, err := contextmanager.NewRollingSummary(store,
		contextmanager.ProviderSummarizer(provider, "Summarize earlier conversation facts concisely."),
		contextmanager.WithMaxInputTokens(2_000),
		contextmanager.WithPreserveRecentTurns(2),
	)
	if err != nil {
		log.Fatal(err)
	}
	a, err := agent.New(provider, "You are helpful and concise.", agent.WithConversationStore(store), agent.WithContextManager(manager))
	if err != nil {
		log.Fatal(err)
	}
	ctx := agent.Background().WithConversationID("summary-demo")
	for _, question := range []string{"My name is Bob and I live in Berlin.", "I work as a software engineer.", "What do you know about me?"} {
		result, err := a.Invoke(ctx, question)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(result.Text)
	}
	// Canonical history remains complete even after a rolling summary is saved.
	snapshot, err := store.Load(ctx, "summary-demo")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("canonical messages=%d revision=%d sequence=%d\n", len(snapshot.Messages), snapshot.Revision, snapshot.LastSequence)
}
