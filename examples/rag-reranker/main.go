// Example: RAG with Bedrock reranker (Amazon Rerank 1.0).
//
// Demonstrates two-stage retrieval: a wide vector search followed by
// cross-encoder reranking for higher-quality results. The example runs
// each query twice — once with plain vector search, once with the
// reranker — and prints both orderings side by side so you can see the
// difference.
//
// Run:
//
//	go run ./rag-reranker

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
	ctx := context.Background()

	// 1. Embedder — Titan Embed V2 (1024 dimensions).
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	// 2. In-memory vector store.
	store := rag.NewMemoryStore()

	// 3. Ingest documents — a mix of topics so the reranker has work to do.
	docs := []string{
		"Go was designed at Google by Robert Griesemer, Rob Pike, and Ken Thompson. It was first released in 2009.",
		"The Go standard library includes packages for HTTP servers, JSON encoding, cryptography, and testing.",
		"Go uses goroutines for concurrency. A goroutine is a lightweight thread managed by the Go runtime.",
		"Rust is a systems programming language focused on safety, speed, and concurrency without a garbage collector.",
		"Python is a high-level, interpreted language known for its readability and extensive standard library.",
		"Kubernetes automates deployment, scaling, and management of containerized applications across clusters.",
		"Docker packages applications into containers that include everything needed to run: code, runtime, and libraries.",
		"PostgreSQL is a powerful open-source relational database with support for JSON, full-text search, and extensions.",
	}

	if err := rag.Ingest(ctx, store, embedder, docs, nil); err != nil {
		log.Fatalf("ingest: %v", err)
	}
	fmt.Printf("Ingested %d documents\n\n", len(docs))

	// 4. Reranker — Amazon Rerank 1.0 via Bedrock.
	reranker := ragbedrock.MustReranker(ragbedrock.AmazonRerank10())

	// 5. Two retrievers: one without reranking, one with.
	baseRetriever := rag.NewRetriever(embedder, store,
		rag.WithTopK(5),
		rag.WithScoreThreshold(0.2),
	)
	rerankedRetriever := rag.NewRetriever(embedder, store,
		rag.WithTopK(5),
		rag.WithScoreThreshold(0.2),
		rag.WithReranker(reranker),
	)

	// 6. Compare retrieval results for each query.
	questions := []string{
		"How does Go handle concurrency?",
		"What is Kubernetes used for?",
		"Which language does not use a garbage collector?",
	}

	for _, q := range questions {
		fmt.Printf("━━━ Q: %s\n\n", q)

		baseDocs, err := baseRetriever.Retrieve(ctx, q)
		if err != nil {
			log.Fatalf("base retrieve: %v", err)
		}
		fmt.Println("  Vector search only:")
		for i, d := range baseDocs {
			fmt.Printf("    %d. %.80s\n", i+1, d.Content)
		}

		rerankedDocs, err := rerankedRetriever.Retrieve(ctx, q)
		if err != nil {
			log.Fatalf("reranked retrieve: %v", err)
		}
		fmt.Println("\n  After reranking:")
		for i, d := range rerankedDocs {
			fmt.Printf("    %d. %.80s\n", i+1, d.Content)
		}
		fmt.Println()
	}

	// 7. Full agent run with the reranked retriever.
	fmt.Println("━━━ Agent answers (with reranker)\n")

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.RAGAgent(
		provider,
		prompt.Text("Answer questions using only the provided context. Be concise."),
		rerankedRetriever,
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	for _, q := range questions {
		result, _, err := a.Invoke(ctx, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Q: %s\nA: %s\n\n", q, result)
	}
}
