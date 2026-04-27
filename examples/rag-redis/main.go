// Example: RAG agent backed by a Redis Stack vector store.
//
// Requires Redis Stack (NOT standard Redis) — the vector store uses RediSearch
// commands (FT.CREATE, FT.SEARCH) that are only available in Redis Stack.
//
// Run this example:
//
//	REDIS_ADDR=localhost:6379 go run ./rag-redis

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	ragredis "github.com/camilbinas/gude-agents/agent/rag/redis"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	store, err := ragredis.New(
		ragredis.Options{Addr: redisAddr},
		"example-docs",
		1024, // Titan Embed V2 outputs 1024 dimensions
		ragredis.WithDropExisting(),
	)
	if err != nil {
		log.Fatalf("redis vectorstore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	docs := []string{
		"Go is a statically typed, compiled language designed at Google. It is syntactically similar to C but with memory safety and garbage collection.",
		"Redis is an in-memory data structure store used as a database, cache, and message broker. It supports strings, hashes, lists, sets, and sorted sets.",
		"Kubernetes is an open-source container orchestration platform that automates deployment, scaling, and management of containerized applications.",
	}

	if err = rag.Ingest(ctx, store, embedder, docs, nil, rag.WithConcurrency(5)); err != nil {
		log.Fatalf("ingest: %v", err)
	}
	fmt.Printf("Ingested %d documents\n", len(docs))

	provider := bedrock.Must(bedrock.Standard())

	retriever := rag.NewRetriever(embedder, store, rag.WithMaxResults(2))

	a, err := agent.RAGAgent(
		provider,
		prompt.Text("Answer questions using only the provided context. Be concise."),
		retriever,
		nil,
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nAsk questions about the ingested documents. Type 'quit' to exit.")
	utils.Chat(ctx, a)
}
