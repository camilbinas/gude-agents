// Example: RAG agent backed by PostgreSQL + pgvector.
//
// Requires PostgreSQL with the pgvector extension installed:
//
//	CREATE EXTENSION IF NOT EXISTS vector;
//
// Environment variables:
//
//	POSTGRES_URL — connection string (required)
//
// Run:
//
//	POSTGRES_URL="postgres://user:pass@localhost:5432/mydb?sslmode=disable" go run ./rag-postgres

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	ragpg "github.com/camilbinas/gude-agents/agent/rag/postgres"
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

	store, err := ragpg.New(pool, 1024,
		ragpg.WithTableName("example_docs"),
		ragpg.WithAutoMigrate(),
	)
	if err != nil {
		log.Fatalf("postgres vectorstore: %v", err)
	}
	defer store.Close()

	// Ingest some sample documents.
	docs := []string{
		"Go is a statically typed, compiled language designed at Google. It is syntactically similar to C but with memory safety and garbage collection.",
		"Redis is an in-memory data structure store used as a database, cache, and message broker. It supports strings, hashes, lists, sets, and sorted sets.",
		"Kubernetes is an open-source container orchestration platform that automates deployment, scaling, and management of containerized applications.",
		"PostgreSQL is a powerful open-source relational database with support for JSON, full-text search, and extensions like pgvector for vector similarity search.",
	}

	fmt.Printf("Ingesting %d documents...\n", len(docs))
	if err = rag.Ingest(ctx, store, embedder, docs, nil); err != nil {
		log.Fatalf("ingest: %v", err)
	}
	fmt.Println("Done.")

	provider := bedrock.Must(bedrock.Standard())

	retriever := rag.NewRetriever(embedder, store, rag.WithMaxResults(2))

	a, err := agent.Default(
		provider,
		prompt.Text("Answer questions using only the provided context. Be concise."),
		nil,
		agent.WithRetriever(retriever),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nPostgres RAG chat (type 'quit' to exit)")
	utils.Chat(ctx, a)
}
