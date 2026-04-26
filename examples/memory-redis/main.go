// Example: Redis-backed episodic memory for persistent long-term knowledge.
//
// Demonstrates episodic memory backed by Redis Stack (RediSearch). Unlike the
// in-memory store, facts persist across process restarts because they are stored
// in Redis with HNSW-indexed embedding vectors for semantic similarity search.
//
// The agent receives two tools:
//   - remember: stores a fact into Redis long-term memory
//   - recall: retrieves relevant facts by semantic search
//
// The identifier is set on the context via agent.WithIdentifier so the tools
// automatically scope storage and retrieval to the correct entity.
//
// Prerequisites:
//   - AWS credentials configured (env vars, ~/.aws/credentials, or IAM role)
//     for the Bedrock embedder
//   - Redis Stack running locally (NOT standard Redis — requires RediSearch)
//
// Start Redis Stack:
//
//	docker run -p 6379:6379 redis/redis-stack-server:latest
//
// Optional env vars:
//   - REDIS_ADDR: Redis address (default: 127.0.0.1:6379)
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
//	go run ./memory-redis
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/memory"
	memoryredis "github.com/camilbinas/gude-agents/agent/memory/redis"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}

	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	// Create a Redis memory store (1024-dim for Titan Embed V2).
	store, err := memoryredis.New(
		memoryredis.Options{Addr: addr},
		embedder,
		1024,
	)
	if err != nil {
		log.Fatalf("redis memory store: %v", err)
	}
	defer store.Close()

	// Create the agent with memory tools.
	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.RISEN{
			Role:         "You are a friendly assistant with long-term memory who remembers everything the user tells you.",
			Instructions: "Use the remember tool to store facts, preferences, and decisions the user shares. Use the recall tool to retrieve relevant context before answering questions.",
			Steps:        "1) When the user shares something worth remembering, store it. 2) Before answering questions, recall relevant context. 3) Respond naturally, weaving recalled facts into the conversation.",
			EndGoal:      "Be a helpful assistant who never forgets and always references what the user has previously shared.",
			Narrowing:    "Keep responses conversational and concise. Don't list raw tool output — synthesize it into a natural answer.",
		},
		[]tool.Tool{
			memory.RememberTool(store),
			memory.RecallTool(store),
		},
		debug.WithLogging(),
		agent.WithConversation(conversation.NewWindow(conversation.NewInMemory(), 40), "redis-episodic-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.WithIdentifier(context.Background(), "user-123")

	fmt.Println()
	fmt.Println("Chat agent with Redis episodic memory. Type 'quit' to exit.")
	fmt.Println("Try: 'Remember that I prefer dark mode' then 'What are my preferences?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
