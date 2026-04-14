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
	"github.com/camilbinas/gude-agents/agent/redis"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	embedder, err := bedrock.TitanEmbedV2()
	if err != nil {
		log.Fatal(err)
	}

	store, err := redis.NewRedisVectorStore(
		redis.RedisOptions{Addr: redisAddr},
		"example-docs", // index name
		1024,           // dimension (Titan Embed V2 outputs 1024)
	)
	if err != nil {
		log.Fatalf("redis vectorstore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Ingest some documents.
	docs := []string{
		"Go is a statically typed, compiled language designed at Google. It is syntactically similar to C but with memory safety and garbage collection.",
		"Redis is an in-memory data structure store used as a database, cache, and message broker. It supports strings, hashes, lists, sets, and sorted sets.",
		"Kubernetes is an open-source container orchestration platform that automates deployment, scaling, and management of containerized applications.",
	}

	err = rag.Ingest(ctx, store, embedder, docs, nil)
	if err != nil {
		log.Fatalf("ingest: %v", err)
	}
	fmt.Printf("Ingested %d documents\n", len(docs))

	// Create a retriever-backed agent.
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

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
