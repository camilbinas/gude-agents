// Run: go run ./conversation
//
// ConversationStore is the complete append-only transcript. ContextManager is
// only the model-facing projection: rolling summaries never rewrite history.
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

const conversationID = "travel-planning"

func main() {
	provider := bedrock.Must(bedrock.Standard())
	store := conversation.NewInMemory()
	manager, err := contextmanager.NewRollingSummary(
		store,
		contextmanager.DefaultProviderSummarizer(provider),
		contextmanager.WithMaxInputTokens(250), // deliberately small for this demo
		contextmanager.WithPreserveRecentTurns(1),
	)
	if err != nil {
		log.Fatal(err)
	}
	a, err := agent.New(provider, "You are a concise travel planner.", agent.WithConversationStore(store), agent.WithContextManager(manager))
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background().WithConversationID(conversationID)
	for _, input := range []string{
		"I will visit Kyoto for five days in April.",
		"I prefer quiet neighborhoods and vegetarian food.",
		"My budget is about 200 dollars per day.",
		"Suggest an itinerary based on my preferences.",
	} {
		result, err := a.Invoke(ctx, input)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Q: %s\nA: %s\n\n", input, result.Text)
	}

	transcript, err := store.Load(ctx, conversationID)
	if err != nil {
		log.Fatal(err)
	}
	state, err := store.LoadContextState(context.Background(), conversationID, "rolling_summary")
	if err != nil {
		log.Fatal(err)
	}
	var summary struct {
		CoveredThrough uint64 `json:"covered_through"`
	}
	_ = json.Unmarshal(state.Data, &summary)
	boundary, _ := manager.HistoryBoundary(ctx, conversationID)
	fmt.Printf("Canonical transcript: %d messages through sequence %d\n", len(transcript.Messages), transcript.LastSequence)
	fmt.Printf("Derived summary covers through %d; future model reads begin after %d\n", summary.CoveredThrough, boundary)
}
