// Example: Episodic memory for long-term user knowledge.
//
// Demonstrates episodic memory — the agent can remember facts, preferences,
// and decisions across conversations using semantic similarity search. Unlike
// conversation memory (which stores message history), episodic memory stores
// discrete knowledge items that persist per user and are recalled by meaning.
//
// The example creates an in-memory episodic store backed by Titan Embed V2
// for vector similarity. The agent receives two tools:
//   - remember: stores a fact into long-term memory
//   - recall: retrieves relevant facts by semantic search
//
// The identifier is set on the context via agent.WithIdentifier so the tools
// automatically scope storage and retrieval to the correct entity.
//
// Prerequisites:
//   - AWS credentials configured (env vars, ~/.aws/credentials, or IAM role)
//
// Sample session:
//
//	You: Remember that I prefer dark mode and use Go as my primary language.
//	Agent: I've stored those preferences for you.
//
//	You: What do you know about my preferences?
//	Agent: You prefer dark mode and use Go as your primary language.
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

	store := memory.NewStore(embedder)

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

	fmt.Println("Chat agent with episodic memory. Type 'quit' to exit.")
	fmt.Println("Try: 'Remember that I prefer dark mode' then 'What are my preferences?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
