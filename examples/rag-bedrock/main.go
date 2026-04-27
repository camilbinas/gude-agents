// Example: Bedrock Knowledge Base retrieval with a RAG agent.
//
// Demonstrates using KnowledgeBaseRetriever to query an AWS Bedrock Knowledge
// Base and feed the results into an agent via NewRetrieverTool. The agent
// decides when to call the retriever based on the user's question.
//
// Prerequisites:
//   - A Bedrock Knowledge Base already created and synced
//   - KNOWLEDGE_BASE_ID env var set to your Knowledge Base ID
//   - AWS_REGION env var set (defaults to us-east-1)
//
// Run:
//
//	KNOWLEDGE_BASE_ID=<your-kb-id> go run ./aws-bedrock-rag

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
	rag "github.com/camilbinas/gude-agents/agent/rag/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	kbID := os.Getenv("KNOWLEDGE_BASE_ID")
	if kbID == "" {
		log.Fatal("KNOWLEDGE_BASE_ID env var is required")
	}

	// Build the retriever — points at your Bedrock Knowledge Base.
	// Fetches up to 5 results and discards anything below 0.4 relevance.
	retriever, err := rag.NewKnowledgeBaseRetriever(kbID,
		rag.WithKnowledgeBaseTopK(5),
		rag.WithKnowledgeBaseScoreThreshold(0.4),
	)
	if err != nil {
		log.Fatalf("retriever: %v", err)
	}

	// Wrap the retriever as a tool the LLM can call on demand.
	kbTool := agent.NewRetrieverTool(
		"search_knowledge_base",
		"Search the knowledge base for relevant information. Use this whenever you need to answer a question.",
		retriever,
	)

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.New(
		provider,
		prompt.Text("You are a helpful assistant. Use the search_knowledge_base tool to find relevant information before answering."),
		[]tool.Tool{kbTool},
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}

	ctx := context.Background()

	fmt.Printf("Knowledge Base agent ready (KB: %s)\n", kbID)
	fmt.Println("Ask a question (or type 'quit' to exit):")

	utils.Chat(ctx, a)
}
