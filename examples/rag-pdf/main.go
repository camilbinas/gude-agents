// Example: RAG pipeline with PDF documents.
//
// Loads all supported files from a directory, ingests them into a vector store,
// and starts an interactive chat where the agent answers questions using the documents.
//
// Run:
//
//	go run ./rag-pdf ./docs
//
// Requires:
//
//	go get github.com/camilbinas/gude-agents/agent/rag/document/pdf

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/camilbinas/gude-agents/agent/rag/document"
	_ "github.com/camilbinas/gude-agents/agent/rag/document/pdf" // enables .pdf support
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := context.Background()

	// 1. Load documents from a directory.
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run ./rag-pdf <directory>")
		os.Exit(1)
	}
	docsDir := os.Args[1]

	fmt.Printf("Loading documents from %s...\n", docsDir)
	texts, metadata, err := document.LoadDir(ctx, docsDir)
	if err != nil {
		log.Fatal(err)
	}
	if len(texts) == 0 {
		log.Fatalf("No supported documents found in %s", docsDir)
	}
	fmt.Printf("Loaded %d document(s):\n", len(texts))
	for _, m := range metadata {
		fmt.Printf("  - %s\n", m["filename"])
	}

	// 2. Create an embedder and vector store.
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())
	store := rag.NewMemoryStore()

	// 3. Ingest — split into chunks, embed, and store.
	fmt.Println("\nIngesting...")
	err = rag.Ingest(ctx, store, embedder, texts, metadata,
		rag.WithChunkSize(768),
		rag.WithChunkOverlap(64),
		rag.WithConcurrency(5),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Done.")

	// 4. Create a retriever and agent.
	retriever := rag.NewRetriever(embedder, store, rag.WithMaxResults(5))

	a, err := agent.RAGAgent(
		bedrock.Must(bedrock.Standard()),
		prompt.RISEN{
			Role:         "You are a knowledgeable document assistant with access to reference documents provided as context.",
			Instructions: "Answer questions based on the provided reference documents. Cite the source filename when possible.",
			Steps:        "1. Read the retrieved context carefully.\n2. Find the most relevant information.\n3. Formulate a concise answer grounded in the documents.",
			EndGoal:      "Provide accurate, concise answers that reference the source material.",
			Narrowing:    "Only use information from the provided documents. If the documents don't contain relevant information, say so.",
		},
		retriever,
		nil,
		agent.WithSharedMemory(memory.NewStore()),
		debug.WithLogging(),
		agent.WithContextFormatter(logAndFormat),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 5. Interactive chat.
	fmt.Println("\nAsk questions about your documents. Type 'quit' to exit.")
	utils.Chat(ctx, a, utils.ChatOptions{ShowUsage: false})
}

// logAndFormat prints the retrieved chunks and returns the formatted context string.
func logAndFormat(docs []agent.Document) string {
	fmt.Printf("\n  📄 Retrieved %d chunk(s):\n", len(docs))
	for i, d := range docs {
		source := d.Metadata["filename"]
		if source == "" {
			source = "unknown"
		}
		preview := d.Content
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		fmt.Printf("     [%d] %s: %s\n", i+1, source, preview)
	}
	fmt.Println()
	return agent.DefaultContextFormatter(docs)
}
