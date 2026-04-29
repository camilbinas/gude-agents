// Run:
//
//	POSTGRES_URL="postgres://user:pass@localhost:5432/mydb?sslmode=disable" go run ./conversation-postgres
//
// DDL (create before running):
//
//	CREATE TABLE agent_conversations (
//	    conversation_id TEXT PRIMARY KEY,
//	    messages        JSONB NOT NULL,
//	    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	);

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation/postgres"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	// Set POSTGRES_URL to your connection string, e.g.:
	//   export POSTGRES_URL="postgres://user:pass@localhost:5432/mydb?sslmode=disable"
	pgURL := os.Getenv("POSTGRES_URL")
	if pgURL == "" {
		log.Fatal("POSTGRES_URL is required")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		log.Fatalf("postgres connect: %v", err)
	}

	store, err := postgres.New(pool,
		postgres.WithTableName("agent_conversations"),
	)
	if err != nil {
		log.Fatalf("postgres store: %v", err)
	}
	defer store.Close()

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithConversation(store, "demo-conversation"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Postgres chat (type 'quit' to exit, 'clear' to reset)")

	utils.Chat(ctx, a, utils.ChatOptions{
		ClearFunc: utils.ClearConversation(store, "demo-conversation"),
	})
}
