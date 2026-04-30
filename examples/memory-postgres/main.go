// Example: Typed PostgreSQL memory with struct-to-column mapping as agent tools.
//
// Demonstrates using TypedStore with Remember/Recall tools so the LLM can
// store and retrieve structured episodic memories with filtered recall.
//
// Prerequisites:
//   - PostgreSQL with pgvector extension
//   - Table created (see DDL below)
//
// DDL:
//
//	CREATE EXTENSION IF NOT EXISTS vector;
//
//	CREATE TABLE episodic_memories (
//	    id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
//	    user_id     TEXT NOT NULL,
//	    title       TEXT NOT NULL,
//	    event_type  TEXT NOT NULL,
//	    context     TEXT,
//	    actions     JSONB,
//	    outcome     TEXT,
//	    importance  DOUBLE PRECISION NOT NULL DEFAULT 0,
//	    observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
//	    embedding   vector(1024) NOT NULL
//	);
//
//	CREATE INDEX episodic_memories_user_idx ON episodic_memories (user_id);
//	CREATE INDEX episodic_memories_embedding_idx ON episodic_memories
//	    USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 200);
//
// Run:
//
//	POSTGRES_URL="postgres://user:pass@localhost:5432/mydb?sslmode=disable" go run ./memory-postgres

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/memory/postgres"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// EpisodicMemory represents a single observed event.
type EpisodicMemory struct {
	ID         string    `json:"id" db:"id,pk"`
	UserID     string    `json:"user_id" db:"user_id,identifier"`
	Title      string    `json:"title" db:"title,content" description:"Short title summarizing the event" required:"true"`
	EventType  string    `json:"event_type" db:"event_type" description:"Category: incident, recovery, deployment, observation" required:"true"`
	Context    string    `json:"context" db:"context" description:"Detailed description of what happened" required:"true"`
	Actions    []string  `json:"actions" db:"actions,jsonb" description:"Actions taken"`
	Outcome    string    `json:"outcome" db:"outcome" description:"Result of the event"`
	Importance float64   `json:"importance" db:"importance" description:"Importance score 0.0-1.0" required:"true"`
	ObservedAt time.Time `json:"observed_at" db:"observed_at,noinput"`
}

func main() {
	godotenv.Load() //nolint

	pgURL := os.Getenv("POSTGRES_URL")
	if pgURL == "" {
		log.Fatal("POSTGRES_URL environment variable is required")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		log.Fatalf("postgres connect: %v", err)
	}
	defer pool.Close()

	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	mem, err := postgres.NewStore[EpisodicMemory](pool, embedder, 1024,
		postgres.WithTableName("episodic_memories"),
	)
	if err != nil {
		log.Fatalf("typed store: %v", err)
	}

	// Create tools — recall defaults to filtering by importance and sorting by time.
	rememberTool := postgres.NewRememberTool(mem,
		postgres.WithToolName("remember_event"),
		postgres.WithToolDescription("Store an observed event as an episodic memory."),
	)
	updateTool := postgres.NewUpdateTool(mem,
		postgres.WithToolName("update_event"),
		postgres.WithToolDescription("Update an existing episodic memory by its ID."),
	)
	recallTool := postgres.NewRecallTool(mem,
		postgres.WithToolName("recall_events"),
		postgres.WithToolDescription("Recall relevant past events by semantic similarity, sorted by most recent."),
		postgres.WithFieldGT("importance", 0.3),
		postgres.WithOrderBy("observed_at", postgres.Desc),
	)
	forgetTool := postgres.NewForgetTool(mem,
		postgres.WithToolName("forget_event"),
		postgres.WithToolDescription("Remove a specific event from memory by its ID."),
	)

	store := conversation.NewWindow(conversation.NewInMemory(), 20)

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text(
			"You are a monitoring assistant that tracks system events. "+
				"Use remember_event to store incidents, recoveries, and deployments. "+
				"Use update_event to correct or enrich an existing event by its ID. "+
				"Use recall_events to retrieve relevant past events when asked. "+
				"Always recall before answering questions about past events.",
		),
		[]tool.Tool{rememberTool, updateTool, recallTool, forgetTool},
		agent.WithConversation(store, "monitoring-session"),
		auto.WithLogging(),
		agent.WithParallelToolExecution(),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx = agent.WithIdentifier(ctx, "user-123")

	fmt.Println()
	fmt.Println("Monitoring assistant with typed episodic memory. Type 'quit' to exit, 'clear' to forget all.")
	fmt.Println("Try: 'example.com went down with HTTP 503' then 'what incidents happened recently?'")
	fmt.Println()

	utils.Chat(ctx, a, utils.ChatOptions{
		ClearFunc: func(ctx context.Context) error {
			if err := store.Delete(ctx, "monitoring-session"); err != nil {
				return err
			}

			return mem.ForgetAll(ctx, "user-123")
		},
	})
}
