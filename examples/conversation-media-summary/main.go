// Run: go run ./conversation-media-summary
//
// This example keeps image turns in the append-only canonical transcript while
// a rolling summary reduces only the provider-facing projection. Applications
// that need vision-aware summaries can supply a custom contextmanager.Summarizer.
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
		contextmanager.ProviderSummarizer(provider, "Summarize prior context; describe image details when present."),
		contextmanager.WithMaxInputTokens(2_000), contextmanager.WithPreserveRecentTurns(2))
	if err != nil {
		log.Fatal(err)
	}
	a, err := agent.New(provider, "You are a helpful vision assistant.", agent.WithConversationStore(store), agent.WithContextManager(manager))
	if err != nil {
		log.Fatal(err)
	}
	ctx := agent.Background().WithConversationID("media-demo")
	result, err := a.Invoke(ctx, "Describe the image I will provide in a later turn.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
	snapshot, _ := store.Load(ctx, "media-demo")
	fmt.Printf("canonical messages remain: %d\n", snapshot.LastSequence)
}
