// Example: Redis-backed memory for persistent long-term knowledge.
//
// Memory backed by Redis Stack (RediSearch) with HNSW-indexed semantic search.
//
// Prerequisites:
//   - Redis Stack running locally (NOT standard Redis — requires RediSearch)
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
			Role:         "You are a friendly assistant with long-term memory.",
			Instructions: "Always recall relevant context at the start of every turn — including greetings. Only remember things the user explicitly asks you to remember, or key preferences and decisions (not casual remarks or small talk).",
			Steps:        "1) At the start of every turn, recall context relevant to the user's message. 2) If the user explicitly shares a preference or asks you to remember something, store it. 3) Respond naturally using recalled context.",
			EndGoal:      "Be a helpful assistant who remembers what matters and greets users personally.",
			Narrowing:    "Keep responses conversational and concise. Don't store trivial exchanges. Don't list raw tool output.",
		},
		[]tool.Tool{
			memory.RememberTool(store),
			memory.RecallTool(store),
		},
		debug.WithLogging(),
		agent.WithConversation(conversation.NewWindow(conversation.NewInMemory(), 40), "redis-memory-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.WithIdentifier(context.Background(), "user-123")

	fmt.Println()
	fmt.Println("Chat agent with Redis memory. Type 'quit' to exit.")
	fmt.Println("Try: 'Remember that I prefer dark mode' then 'What are my preferences?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
