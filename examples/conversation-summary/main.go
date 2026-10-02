// Run: go run ./conversation-summary
//
// Demonstrates the rolling-summary context manager actually firing: a small
// input-token budget forces earlier turns to be summarized into durable
// derived state while the canonical transcript stays complete and verbatim.
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

const conversationID = "summary-demo"

func main() {
	provider := bedrock.Must(bedrock.Standard())
	store := conversation.NewInMemory()
	manager, err := contextmanager.NewRollingSummary(store,
		// DefaultProviderSummarizer uses the batteries-included summary prompt.
		contextmanager.DefaultProviderSummarizer(provider),
		// A deliberately small budget so the summarizer fires after a few
		// turns, and only the single most recent turn is kept verbatim.
		contextmanager.WithMaxInputTokens(200),
		contextmanager.WithPreserveRecentTurns(1),
	)
	if err != nil {
		log.Fatal(err)
	}
	a, err := agent.New(provider, "You are helpful and concise.",
		agent.WithConversationStore(store),
		agent.WithContextManager(manager),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background().WithConversationID(conversationID)
	questions := []string{
		"My name is Bob and I live in Berlin.",
		"I work as a software engineer, mostly in Go.",
		"My favorite hobby is bouldering on weekends.",
		"I'm planning a trip to Japan next spring.",
		"What do you know about me?",
	}
	for i, question := range questions {
		result, err := a.Invoke(ctx, question)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Q%d: %s\nA%d: %s\n\n", i+1, question, i+1, result.Text)
		reportSummaryState(ctx, store, manager)
	}

	// Canonical history remains complete even after rolling summaries are saved.
	snapshot, err := store.Load(ctx, conversationID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("canonical messages=%d revision=%d sequence=%d\n", len(snapshot.Messages), snapshot.Revision, snapshot.LastSequence)
}

// reportSummaryState prints the derived rolling-summary state so the
// summarization is observable: the boundary advances as earlier turns are
// folded into the durable summary, while canonical history is untouched.
func reportSummaryState(ctx context.Context, store *conversation.InMemory, manager *contextmanager.RollingSummary) {
	boundary, err := manager.HistoryBoundary(ctx, conversationID)
	if err != nil {
		log.Fatal(err)
	}
	snap, err := store.LoadContextState(ctx, conversationID, "rolling_summary")
	if err != nil {
		log.Fatal(err)
	}
	if len(snap.Data) == 0 {
		fmt.Printf("   [summary] not triggered yet (boundary=%d)\n\n", boundary)
		return
	}
	var state struct {
		Summary        string `json:"summary"`
		CoveredThrough uint64 `json:"covered_through"`
	}
	if err := json.Unmarshal(snap.Data, &state); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   [summary] covered_through=%d boundary=%d\n   summary: %s\n\n", state.CoveredThrough, boundary, state.Summary)
}
