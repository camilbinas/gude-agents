// Run: go run ./rag
//
// Ingest a few documents into an in-memory vector store, create a Retriever,
// and let RAGAgent add matching context transiently to each model request.
package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
)

func main() {
	ctx := agent.Background()
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())
	store := rag.NewMemoryStore()
	docs := []string{
		"Go was designed at Google by Robert Griesemer, Rob Pike, and Ken Thompson.",
		"Go uses goroutines: lightweight concurrent functions managed by the runtime.",
		"The standard library includes HTTP, JSON, cryptography, and testing packages.",
	}
	if err := rag.Ingest(ctx, store, embedder, docs, nil); err != nil {
		log.Fatal(err)
	}

	retriever := rag.NewRetriever(embedder, store, rag.WithMaxResults(2))
	a, err := agent.RAGAgent(bedrock.Must(bedrock.Standard()), "Answer only from retrieved context.", retriever)
	if err != nil {
		log.Fatal(err)
	}
	result, err := a.Invoke(ctx, "How does Go handle concurrency?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
