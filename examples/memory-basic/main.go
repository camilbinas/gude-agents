// Example: Memory for long-term user knowledge.
//
// The agent remembers facts and preferences across conversations using an
// in-memory vector store backed by Titan Embed V2.
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
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	store := memory.NewInMemory(embedder)

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a helpful assistant with long-term memory. "+
			"Use the remember tool to store important facts, preferences, and decisions the user shares. "+
			"Use the recall tool to retrieve relevant context before answering questions."),
		[]tool.Tool{
			memory.RememberTool(store),
			memory.RecallTool(store),
		},
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.WithIdentifier(context.Background(), "user-123")

	fmt.Println("Chat agent with memory. Type 'quit' to exit.")
	fmt.Println("Try: 'Remember that I prefer dark mode' then 'What are my preferences?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
