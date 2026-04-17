// Example: OpenAI Vector Store retrieval agent.
//
// Uses VectorStoreRetriever to search an OpenAI Vector Store and answer
// questions using GPT-4o. The agent calls the retriever tool whenever it
// needs to look something up.
//
// Prerequisites:
//   - OPENAI_API_KEY env var set
//   - VECTOR_STORE_ID env var set to your vs_xxx ID
//     (create one at https://platform.openai.com/storage/vector_stores)
//
// Run:
//
//	OPENAI_API_KEY=sk-... VECTOR_STORE_ID=vs_... go run ./openai-rag

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/openai"
	ragopenai "github.com/camilbinas/gude-agents/agent/rag/openai"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	vsID := os.Getenv("VECTOR_STORE_ID")
	if vsID == "" {
		log.Fatal("VECTOR_STORE_ID env var is required")
	}

	// Retriever backed by the OpenAI Vector Store.
	// Returns up to 5 chunks; drops anything below 0.4 relevance.
	retriever, err := ragopenai.NewVectorStoreRetriever(vsID,
		ragopenai.WithVectorStoreTopK(5),
		ragopenai.WithVectorStoreScoreThreshold(0.4),
	)
	if err != nil {
		log.Fatalf("retriever: %v", err)
	}

	// Wrap the retriever with a logging layer so every search is printed.
	logged := &loggingRetriever{inner: retriever}

	// Wrap as a tool so the model decides when to search.
	kbTool := agent.NewRetrieverTool(
		"search_knowledge_base",
		"Search the knowledge base for relevant information.",
		logged,
	)

	provider, err := openai.Standard()
	if err != nil {
		log.Fatalf("provider: %v", err)
	}

	a, err := agent.New(
		provider,
		prompt.Text("You are a helpful assistant. "+
			"You MUST call the search_knowledge_base tool before answering any question. "+
			"Answer ONLY using information returned by the tool. "+
			"If the tool returns no relevant results, say you don't have that information in the knowledge base. "+
			"Do NOT use your own prior knowledge or make anything up."),
		[]tool.Tool{kbTool},
	)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}

	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Printf("Vector Store agent ready (vector store: %s)\n\n", vsID)
	fmt.Println("Ask a question (or type 'quit' to exit):")

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		q := strings.TrimSpace(scanner.Text())
		if q == "" {
			continue
		}
		if strings.EqualFold(q, "quit") || strings.EqualFold(q, "exit") {
			break
		}

		result, usage, err := a.Invoke(ctx, q)
		if err != nil {
			log.Printf("error: %v\n\n", err)
			continue
		}

		fmt.Printf("\n%s\n", result)
		fmt.Printf("(tokens: %d in / %d out)\n\n", usage.InputTokens, usage.OutputTokens)
	}
}

// loggingRetriever wraps any agent.Retriever and prints each result to stdout.
type loggingRetriever struct {
	inner agent.Retriever
}

func (l *loggingRetriever) Retrieve(ctx context.Context, query string) ([]agent.Document, error) {
	docs, err := l.inner.Retrieve(ctx, query)
	if err != nil {
		return nil, err
	}

	fmt.Printf("\n── Retrieved %d doc(s) for %q ──\n", len(docs), query)
	for i, doc := range docs {
		score := doc.Metadata["score"]
		filename := doc.Metadata["filename"]
		content := doc.Content
		if len(content) > 150 {
			content = content[:150] + "…"
		}
		fmt.Printf("  [%d] score=%s file=%s\n      %s\n", i+1, score, filename, content)
	}
	fmt.Println("────────────────────────────────────────")

	return docs, nil
}
