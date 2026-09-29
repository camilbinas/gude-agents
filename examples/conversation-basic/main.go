// Run:
//
//	go run ./conversation-basic

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
	store := conversation.NewInMemory()

	a, err := agent.New(
		provider,
		"You are a friendly assistant. Remember details the user shares.",
		agent.WithConversationStore(store),
		agent.WithMaxIterations(10),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background().WithConversationID("chat-session-1")
	turns := []string{
		"Hi, my name is Alice and I love hiking.",
		"What's a good trail for beginners?",
		"What's my name and what do I enjoy?",
	}

	for i, msg := range turns {
		result, err := a.Invoke(ctx, msg)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Turn %d: %s\n\n", i+1, result.Text)
	}
}
