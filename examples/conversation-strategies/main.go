// Run: go run ./conversation-strategies
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
	window, err := contextmanager.NewWindow(store, 20)
	if err != nil {
		log.Fatal(err)
	}
	// Context strategies affect only provider input. The canonical store still
	// retains every message and never has a projected transcript written back.
	manager := contextmanager.NewFilter(window)
	a, err := agent.New(provider, "You are a helpful assistant.", agent.WithConversationStore(store), agent.WithContextManager(manager))
	if err != nil {
		log.Fatal(err)
	}
	ctx := agent.Background().WithConversationID("demo-conversation")
	for _, q := range []string{"My name is Alice. Remember that.", "What is my name?"} {
		result, err := a.Invoke(ctx, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(result.Text)
	}
	snapshot, _ := store.Load(ctx, "demo-conversation")
	fmt.Printf("canonical events: %d\n", snapshot.LastSequence)
}
