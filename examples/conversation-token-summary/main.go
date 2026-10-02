// Run: go run ./conversation-token-summary
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
		contextmanager.ProviderSummarizer(provider, "Preserve facts, names, decisions and open tasks."),
		contextmanager.WithTokenEstimator(agent.CharEstimator{}),
		contextmanager.WithMaxInputTokens(600),
		contextmanager.WithPreserveRecentTurns(2),
	)
	if err != nil {
		log.Fatal(err)
	}
	a, err := agent.New(provider, "You are a concise assistant.", agent.WithConversationStore(store), agent.WithContextManager(manager))
	if err != nil {
		log.Fatal(err)
	}
	ctx := agent.Background().WithConversationID("token-summary-demo")
	for _, q := range []string{"My name is Alice and I live in Tokyo.", "I have two cats named Mochi and Sushi.", "What are my cats' names?"} {
		result, err := a.Invoke(ctx, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("[%d tokens] %s\n", result.Usage.InputTokens, result.Text)
	}
}
