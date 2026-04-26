// Example: Composable episodic memory using building blocks.
//
// Demonstrates the composable approach to episodic memory — instead of using
// the EpisodicMemory interface, this example wires together the building blocks
// directly: VectorStore → ScopedStore → NewRememberTool / NewRecallTool.
//
// This gives you full control over the storage backend and tool configuration
// while keeping the same remember/recall UX for the LLM.
//
// The composable approach is recommended for new code. The EpisodicMemory
// interface (used in the episodic-memory example) remains available for
// backward compatibility.
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
//	go run ./memory-composable
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	// 1. Create a VectorStore and wrap it with scoping.
	vectorStore := rag.NewMemoryStore()
	scopedStore := rag.NewScopedStore(vectorStore)

	// 2. Create composable remember/recall tools.
	tools := []tool.Tool{
		memory.NewRememberTool(scopedStore, embedder),
		memory.NewRecallTool(scopedStore, embedder),
	}

	// 3. Build the agent.
	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.RISEN{
			Role:         "You are a friendly assistant with long-term memory who remembers everything the user tells you.",
			Instructions: "Use the remember tool to store facts, preferences, and decisions the user shares. Use the recall tool to retrieve relevant context before answering questions.",
			Steps:        "1) When the user shares something worth remembering, store it. 2) Before answering questions, recall relevant context. 3) Respond naturally, weaving recalled facts into the conversation.",
			EndGoal:      "Be a helpful assistant who never forgets and always references what the user has previously shared.",
			Narrowing:    "Keep responses conversational and concise. Don't list raw tool output — synthesize it into a natural answer.",
		},
		tools,
		debug.WithLogging(),
		agent.WithConversation(conversation.NewWindow(conversation.NewInMemory(), 40), "composable-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Set the identifier on the context.
	ctx := agent.WithIdentifier(context.Background(), "user-123")

	fmt.Println("Chat agent with composable episodic memory. Type 'quit' to exit.")
	fmt.Println("Try: 'Remember that I prefer dark mode' then 'What are my preferences?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
