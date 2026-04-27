// Example: PostgreSQL-backed memory for persistent long-term knowledge.
//
// Memory backed by PostgreSQL with pgvector for HNSW-indexed semantic search.
//
// Prerequisites:
//   - PostgreSQL with the pgvector extension installed
//
// Run:
//
//	POSTGRES_URL="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" go run ./memory-postgres
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
	memorypostgres "github.com/camilbinas/gude-agents/agent/memory/postgres"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	pgURL := os.Getenv("POSTGRES_URL")
	if pgURL == "" {
		log.Fatal("POSTGRES_URL is required")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		log.Fatalf("postgres connect: %v", err)
	}

	// Titan Embed V2 outputs 1024-dimensional vectors.
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	// Create a PostgreSQL memory store (1024-dim for Titan Embed V2).
	store, err := memorypostgres.New(pool, embedder, 1024,
		memorypostgres.WithAutoMigrate(),
		memorypostgres.WithDropExisting(),
	)
	if err != nil {
		log.Fatalf("postgres memory store: %v", err)
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
		agent.WithConversation(conversation.NewWindow(conversation.NewInMemory(), 40), "postgres-memory-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx = agent.WithIdentifier(ctx, "user-123")

	fmt.Println()
	fmt.Println("Chat agent with PostgreSQL memory. Type 'quit' to exit.")
	fmt.Println("Try: 'Remember that I prefer dark mode' then 'What are my preferences?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
