// Example: RAG agent backed by a Redis Stack vector store.
//
// Requires Redis Stack (NOT standard Redis) — the vector store uses RediSearch
// commands (FT.CREATE, FT.SEARCH) that are only available in Redis Stack.
//
// Run Redis Stack locally:
//
//	docker run -p 6379:6379 redis/redis-stack-server:latest
//
// Then run this example:
//
//	REDIS_ADDR=localhost:6379 go run ./rag-redis

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
	ragredis "github.com/camilbinas/gude-agents/agent/rag/redis"
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

	if err = rag.Ingest(ctx, store, embedder, docs, nil); err != nil {
		log.Fatalf("ingest: %v", err)
	}
	fmt.Printf("Ingested %d documents\n", len(docs))

	provider := bedrock.Must(bedrock.Standard())

	retriever := rag.NewRetriever(embedder, store, rag.WithTopK(2))

	a, err := agent.Default(
		provider,
		prompt.Text("Answer questions using only the provided context. Be concise."),
		nil,
		agent.WithRetriever(retriever),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, _, err := a.Invoke(ctx, "What is Redis used for?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Answer:", result)
}
