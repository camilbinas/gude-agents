// Example: Structured output with RAG.
//
// A product knowledge base is ingested into an in-memory vector store using
// Titan Embeddings. For each query, the RAG pipeline retrieves the most
// relevant product chunks and injects them into the prompt. InvokeStructured
// then forces the model to return a typed ProductSummary — no free-form text,
// just the schema.
//
// Key concepts demonstrated:
//   - rag.Ingest        — chunk, embed, and store documents
//   - rag.NewRetriever  — embed query + cosine similarity search
//   - agent.RAGAgent    — agent with automatic context injection on every call
//   - InvokeStructured  — force a typed JSON response via tool-forcing
//
// Run:
//
//	go run ./rag-structured-output

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
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/joho/godotenv"
)

// ProductSummary is the structured output extracted from retrieved product docs.
type ProductSummary struct {
	Name           string   `json:"name"           description:"Product display name"                                    required:"true"`
	Category       string   `json:"category"       description:"Product category"                                        enum:"electronics,clothing,food,furniture,sports,other" required:"true"`
	PriceUSD       float64  `json:"price_usd"      description:"Price in USD"                                            required:"true"`
	InStock        bool     `json:"in_stock"       description:"Whether the product is currently in stock"               required:"true"`
	Rating         float64  `json:"rating"         description:"Average customer rating from 1.0 to 5.0"                required:"true"`
	KeyFeatures    []string `json:"key_features"   description:"Up to 3 most important product features"                 required:"true"`
	Recommendation string   `json:"recommendation" description:"Buy, wait, or skip — with a one-sentence justification" required:"true"`
}

// knowledge base — in a real system this would come from a database, files, or an API.
var products = []struct {
	name string
	text string
}{
	{
		name: "Nova ViewPro 9000",
		text: `Nova ViewPro 9000 4K Monitor. Category: electronics. Price: $649.99. In stock: yes (42 units).
Average rating: 4.7/5 from 1823 reviews. Resolution: 3840x2160. Refresh rate: 144Hz. Panel: IPS.
HDR: HDR600. Ports: HDMI 2.1, DisplayPort 1.4, USB-C 90W. Last price drop: 2 weeks ago.
Excellent for gaming and creative work. Highly recommended for its color accuracy and fast response time.`,
	},
	{
		name: "ErgoMax Chair X",
		text: `ErgoMax Chair X. Category: furniture. Price: $389.00. In stock: no (restock ETA: 3 weeks).
Average rating: 3.9/5 from 412 reviews. Features: lumbar support, 4D adjustable armrests.
Max weight: 120kg. Warranty: 2 years. Mixed reviews — some users report quality control issues.
Consider waiting for restock or looking at alternatives given the below-average rating.`,
	},
	{
		name: "TrailBlazer X3 Hiking Boots",
		text: `TrailBlazer X3 Hiking Boots. Category: sports. Price: $189.00. In stock: yes (18 units).
Average rating: 4.5/5 from 634 reviews. Waterproof Gore-Tex membrane. Vibram outsole.
Available in sizes 6-14. Weight: 480g per boot. Ankle support: high. Break-in period: minimal.
Great value for serious hikers. Highly recommended for multi-day trails.`,
	},
}

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	// ── Embedder + vector store ───────────────────────────────────────────────
	embedder, err := bedrock.TitanEmbedV2()
	if err != nil {
		log.Fatal(err)
	}

	store := rag.NewMemoryStore()

	// Ingest all product texts into the vector store.
	texts := make([]string, len(products))
	metadata := make([]map[string]string, len(products))
	for i, p := range products {
		texts[i] = p.text
		metadata[i] = map[string]string{"product": p.name}
	}

	fmt.Println("Ingesting product knowledge base...")
	if err := rag.Ingest(ctx, store, embedder, texts, metadata,
		rag.WithChunkSize(300),
		rag.WithChunkOverlap(50),
	); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Done.")

	// ── RAG retriever ─────────────────────────────────────────────────────────
	retriever := rag.NewRetriever(embedder, store,
		rag.WithTopK(3),
		rag.WithScoreThreshold(0.4),
	)

	// ── Agent ─────────────────────────────────────────────────────────────────
	// RAGAgent injects retrieved context into the system prompt automatically
	// before every call — no tool needed, the model always sees relevant docs.
	provider, err := bedrock.NovaPro()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.RAGAgent(
		provider,
		prompt.Text("You are a product analyst. Use the provided context to answer questions about products accurately."),
		retriever,
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Interactive query loop ────────────────────────────────────────────────
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("\nAsk a question about any product (or type 'quit' to exit):")

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

		// Log retriever results before invoking the agent.
		fmt.Println("\n── Retriever results ──────────────────────────────────────")
		docs, err := retriever.Retrieve(ctx, q)
		if err != nil {
			log.Printf("retriever error: %v\n", err)
		} else {
			for i, doc := range docs {
				product := doc.Metadata["product"]
				chunk := doc.Metadata["chunk_index"]
				// Truncate long content for readability.
				content := doc.Content
				if len(content) > 120 {
					content = content[:120] + "…"
				}
				fmt.Printf("  [%d] product=%s chunk=%s\n      %s\n", i+1, product, chunk, content)
			}
		}
		fmt.Println("───────────────────────────────────────────────────────────")

		// InvokeStructured retrieves context via the RAG pipeline, injects it
		// into the prompt, then forces the model to return a typed ProductSummary.
		summary, usage, err := agent.InvokeStructured[ProductSummary](ctx, a, q)
		if err != nil {
			log.Printf("error: %v\n\n", err)
			continue
		}

		fmt.Printf("  Name:           %s\n", summary.Name)
		fmt.Printf("  Category:       %s\n", summary.Category)
		fmt.Printf("  Price:          $%.2f\n", summary.PriceUSD)
		fmt.Printf("  In stock:       %v\n", summary.InStock)
		fmt.Printf("  Rating:         %.1f / 5.0\n", summary.Rating)
		fmt.Printf("  Key features:\n")
		for _, f := range summary.KeyFeatures {
			fmt.Printf("    - %s\n", f)
		}
		fmt.Printf("  Recommendation: %s\n", summary.Recommendation)
		fmt.Printf("  Tokens:         %d in / %d out\n\n", usage.InputTokens, usage.OutputTokens)

		fmt.Println("Ask another question (or type 'quit' to exit):")
	}
}
