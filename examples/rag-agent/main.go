package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	ragbedrock "github.com/camilbinas/gude-agents/agent/rag/bedrock"
)

func main() {
	embedder, err := ragbedrock.TitanEmbedV2()
	if err != nil {
		log.Fatal(err)
	}

	// In-memory vector store for the example.
	store := rag.NewMemoryStore()

	ctx := context.Background()

	// Ingest some documents.
	docs := []string{
		"Go was designed at Google by Robert Griesemer, Rob Pike, and Ken Thompson. It was first released in 2009.",
		"The Go standard library includes packages for HTTP servers, JSON encoding, cryptography, and testing.",
		"Go uses goroutines for concurrency. A goroutine is a lightweight thread managed by the Go runtime.",
	}

	if err := rag.Ingest(ctx, store, embedder, docs, nil); err != nil {
		log.Fatalf("ingest: %v", err)
	}
	fmt.Printf("Ingested %d documents\n\n", len(docs))

	// RAGAgent preset enforces the retriever at the call site.
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	retriever := rag.NewRetriever(embedder, store, rag.WithTopK(2))

	a, err := agent.RAGAgent(
		provider,
		prompt.Text("Answer questions using only the provided context. Be concise."),
		retriever,
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	questions := []string{
		"Who designed Go?",
		"How does Go handle concurrency?",
	}

	for _, q := range questions {
		result, _, err := a.Invoke(ctx, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Q: %s\nA: %s\n\n", q, result)
	}
}
