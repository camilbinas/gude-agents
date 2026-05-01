// Example: In-memory typed memory with remember, recall, and forget.
//
// Demonstrates the simplest memory setup using the in-memory store.
// Type "clear" to forget all memories for the current user.
//
// Run:
//
//	go run ./memory-basic

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
)

// Fact is a simple memory entry — just a text fact with a category.
type Fact struct {
	ID       string `json:"id" db:"id,pk"`
	UserID   string `json:"user_id" db:"user_id,identifier"`
	Text     string `json:"fact" db:"fact,content" description:"The fact, preference, or decision to remember" required:"true"`
	Category string `json:"category" db:"category" description:"Optional category: preference, decision, context"`
}

func main() {
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	mem, err := memory.NewStore[Fact](embedder)
	if err != nil {
		log.Fatal(err)
	}

	store := conversation.NewWindow(conversation.NewInMemory(), 40)

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text(
			"You are a helpful assistant with long-term memory. "+
				"Use remember to store facts, preferences, and decisions the user shares. "+
				"Use recall to retrieve relevant context before answering questions. "+
				"Use forget to remove a specific memory when the user asks you to forget something.",
		),
		[]tool.Tool{
			memory.NewRememberTool(mem),
			memory.NewRecallTool(mem),
			memory.NewForgetTool(mem),
		},
		auto.WithLogging(),
		agent.WithConversation(store, "memory-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background().WithIdentifier("user-123")

	fmt.Println("Chat agent with in-memory memory. Type 'quit' to exit, 'clear' to forget all.")
	fmt.Println("Try: 'Remember that I prefer dark mode' then 'What are my preferences?'")
	fmt.Println("Then: 'Forget that preference'")
	fmt.Println()

	utils.Chat(ctx, a, utils.ChatOptions{
		ClearFunc: func(ctx context.Context) error {
			if err := mem.ForgetAll(ctx, "user-123"); err != nil {
				return err
			}

			return mem.ForgetAll(ctx, "user-123")
		},
	})
}
