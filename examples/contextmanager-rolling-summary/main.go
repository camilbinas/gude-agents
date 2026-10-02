// Run: go run ./contextmanager-rolling-summary
//
// A rolling summary is durable derived state. It covers older immutable events
// and lets normal model calls range-read only the recent canonical suffix.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/contextmanager"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

const conversationID = "rolling-summary-demo"

func main() {
	provider := bedrock.Must(bedrock.Standard())
	store := conversation.NewInMemory()
	manager, err := contextmanager.NewRollingSummary(
		store,
		contextmanager.DefaultProviderSummarizer(provider),
		// Deliberately small so the example summarizes after a few turns.
		contextmanager.WithMaxInputTokens(250),
		contextmanager.WithPreserveRecentTurns(1),
	)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.New(
		provider,
		"You are a helpful travel planner.",
		agent.WithConversationStore(store),
		agent.WithContextManager(manager),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background().WithConversationID(conversationID)
	for _, question := range []string{
		"I will visit Kyoto for five days in April.",
		"I prefer quiet neighborhoods and vegetarian food.",
		"My budget is about 200 dollars per day.",
		"Suggest an itinerary based on my preferences.",
	} {
		result, err := a.Invoke(ctx, question)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Q: %s\nA: %s\n\n", question, result.Text)
		printSummaryState(ctx, store, manager)
	}

	snapshot, err := store.Load(ctx, conversationID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Canonical transcript remains complete: %d messages, sequence %d\n",
		len(snapshot.Messages), snapshot.LastSequence)
}

func printSummaryState(ctx context.Context, store *conversation.InMemory, manager *contextmanager.RollingSummary) {
	boundary, err := manager.HistoryBoundary(ctx, conversationID)
	if err != nil {
		log.Fatal(err)
	}
	state, err := store.LoadContextState(ctx, conversationID, "rolling_summary")
	if err != nil {
		log.Fatal(err)
	}
	if len(state.Data) == 0 {
		fmt.Println("Rolling summary has not triggered yet.")
		return
	}
	var decoded struct {
		CoveredThrough uint64 `json:"covered_through"`
		Summary        string `json:"summary"`
	}
	if err := json.Unmarshal(state.Data, &decoded); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Summary covers through sequence %d (range-read boundary %d): %s\n",
		decoded.CoveredThrough, boundary, decoded.Summary)
}
