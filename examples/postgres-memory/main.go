// Run:
//
//	POSTGRES_URL="postgres://user:pass@localhost:5432/mydb?sslmode=disable" go run ./postgres-memory

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory/postgres"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
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

	mem, err := postgres.New(pool,
		postgres.WithTableName("agent_conversations"),
		postgres.WithAutoMigrate(),
	)
	if err != nil {
		log.Fatalf("postgres memory: %v", err)
	}
	defer mem.Close()

	provider := bedrock.Must(bedrock.ClaudeSonnet4_6())

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithMemory(mem, "demo-conversation"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Postgres memory chat (type 'quit' to exit, 'clear' to reset)")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "quit" {
			break
		}
		if input == "clear" {
			if err := mem.Delete(ctx, "demo-conversation"); err != nil {
				fmt.Printf("Error clearing: %v\n", err)
			} else {
				fmt.Println("Conversation cleared.")
			}
			continue
		}

		usage, err := a.InvokeStream(ctx, input, func(chunk string) {
			fmt.Print(chunk)
		})
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			continue
		}
		fmt.Printf("\n--- tokens: %d in / %d out ---\n", usage.InputTokens, usage.OutputTokens)
	}
}
