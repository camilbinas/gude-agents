// Run: go run ./contextmanager-window
//
// This example makes a ContextManager window visible: the provider receives a
// short tail, while the append-only canonical transcript retains every turn.
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
	window, err := contextmanager.NewWindow(store, 4)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.New(
		provider,
		"You are a concise project assistant.",
		agent.WithConversationStore(store),
		agent.WithContextManager(window),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background().WithConversationID("window-demo")
	for _, question := range []string{
		"Our project is named Atlas.",
		"The release date is June 1.",
		"The owner is Priya.",
		"What project, owner, and release date do you remember?",
	} {
		result, err := a.Invoke(ctx, question)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Q: %s\nA: %s\n\n", question, result.Text)
	}

	// Window affects only future provider requests. All messages remain
	// available to audit/export APIs and can be re-projected differently.
	snapshot, err := store.Load(ctx, "window-demo")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Canonical transcript: %d messages, revision %d, last sequence %d\n",
		len(snapshot.Messages), snapshot.Revision, snapshot.LastSequence)
}
