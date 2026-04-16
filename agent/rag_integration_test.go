//go:build integration

package agent_test

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/rag"
	ragbedrock "github.com/camilbinas/gude-agents/agent/rag/bedrock"
	ragopenai "github.com/camilbinas/gude-agents/agent/rag/openai"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// RAG integration tests that call real embedding and LLM APIs.
//
// Run with:
//   go test -tags=integration -v -timeout=120s -run TestIntegration_RAG ./agent/...
//
// Environment variables:
//   EMBEDDER         - "bedrock" (default) or "openai"
//   BEDROCK_MODEL    - Bedrock LLM model (default: eu.anthropic.claude-sonnet-4-20250514-v1:0)
//   BEDROCK_EMBED    - Bedrock embedding model (default: amazon.titan-embed-text-v2:0)
//   AWS_REGION       - AWS region (default: eu-central-1)
//   OPENAI_API_KEY   - Required when EMBEDDER=openai

func newTestEmbedder(t *testing.T) agent.Embedder {
	t.Helper()
	name := os.Getenv("EMBEDDER")
	if name == "" {
		name = "bedrock"
	}
	switch name {
	case "bedrock":
		model := os.Getenv("BEDROCK_EMBED")
		if model == "" {
			model = "amazon.titan-embed-text-v2:0"
		}
		region := os.Getenv("AWS_REGION")
		if region == "" {
			region = "eu-central-1"
		}
		e, err := ragbedrock.NewEmbedder(model, ragbedrock.WithRegion(region))
		if err != nil {
			t.Fatalf("failed to create bedrock embedder: %v", err)
		}
		t.Logf("Using embedder: bedrock (model=%s, region=%s)", model, region)
		return e
	case "openai":
		e, err := ragopenai.NewEmbedder()
		if err != nil {
			t.Fatalf("failed to create openai embedder: %v", err)
		}
		t.Logf("Using embedder: openai (model=text-embedding-3-small)")
		return e
	default:
		t.Fatalf("unknown EMBEDDER=%q (supported: bedrock, openai)", name)
		return nil
	}
}

// ---------------------------------------------------------------------------
// Embedder smoke tests
// ---------------------------------------------------------------------------

func TestIntegration_RAG_BedrockEmbedder(t *testing.T) {
	if os.Getenv("EMBEDDER") != "" && os.Getenv("EMBEDDER") != "bedrock" {
		t.Skip("skipping bedrock embedder test (EMBEDDER != bedrock)")
	}
	model := os.Getenv("BEDROCK_EMBED")
	if model == "" {
		model = "amazon.titan-embed-text-v2:0"
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "eu-central-1"
	}
	e, err := ragbedrock.NewEmbedder(model, ragbedrock.WithRegion(region))
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	vec, err := e.Embed(ctx, "The quick brown fox jumps over the lazy dog.")
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if len(vec) == 0 {
		t.Fatal("expected non-empty embedding vector")
	}
	t.Logf("Bedrock embedding dimension: %d", len(vec))

	// Verify the vector is normalised (L2 norm ≈ 1.0).
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if math.Abs(norm-1.0) > 0.01 {
		t.Errorf("expected normalised vector (L2 ≈ 1.0), got norm=%f", norm)
	}
}

func TestIntegration_RAG_OpenAIEmbedder(t *testing.T) {
	if os.Getenv("EMBEDDER") != "openai" {
		t.Skip("skipping openai embedder test (set EMBEDDER=openai)")
	}
	e, err := ragopenai.NewEmbedder()
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	vec, err := e.Embed(ctx, "The quick brown fox jumps over the lazy dog.")
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if len(vec) == 0 {
		t.Fatal("expected non-empty embedding vector")
	}
	t.Logf("OpenAI embedding dimension: %d", len(vec))
}

// ---------------------------------------------------------------------------
// Semantic similarity sanity check
// ---------------------------------------------------------------------------

func TestIntegration_RAG_SemanticSimilarity(t *testing.T) {
	embedder := newTestEmbedder(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	vecA, err := embedder.Embed(ctx, "I love programming in Go")
	if err != nil {
		t.Fatalf("Embed A: %v", err)
	}
	vecB, err := embedder.Embed(ctx, "Go is my favourite programming language")
	if err != nil {
		t.Fatalf("Embed B: %v", err)
	}
	vecC, err := embedder.Embed(ctx, "The weather in Paris is sunny today")
	if err != nil {
		t.Fatalf("Embed C: %v", err)
	}

	simAB := cosine(vecA, vecB)
	simAC := cosine(vecA, vecC)

	t.Logf("sim(Go+Go) = %.4f, sim(Go+weather) = %.4f", simAB, simAC)

	if simAB <= simAC {
		t.Errorf("expected similar sentences to score higher: sim(A,B)=%f <= sim(A,C)=%f", simAB, simAC)
	}
}

func cosine(a, b []float64) float64 {
	n := len(a)
	if n != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	d := math.Sqrt(na) * math.Sqrt(nb)
	if d == 0 {
		return 0
	}
	return dot / d
}

// ---------------------------------------------------------------------------
// Full RAG pipeline: ingest → retrieve → verify
// ---------------------------------------------------------------------------

func TestIntegration_RAG_IngestAndRetrieve(t *testing.T) {
	embedder := newTestEmbedder(t)
	store := rag.NewMemoryStore()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Ingest a small corpus about programming languages.
	texts := []string{
		"Go is a statically typed, compiled language designed at Google. It is known for its simplicity and concurrency support.",
		"Python is a dynamically typed, interpreted language popular for data science and machine learning applications.",
		"Rust is a systems programming language focused on safety, speed, and concurrency without a garbage collector.",
		"JavaScript is the language of the web, running in browsers and on servers via Node.js.",
	}
	metadata := []map[string]string{
		{"source": "go-docs"},
		{"source": "python-docs"},
		{"source": "rust-docs"},
		{"source": "js-docs"},
	}

	err := rag.Ingest(ctx, store, embedder, texts, metadata,
		rag.WithChunkSize(200),
		rag.WithChunkOverlap(0),
	)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}

	// Build a retriever and query for Go-related content.
	retriever := rag.NewRetriever(embedder, store, rag.WithTopK(2))

	docs, err := retriever.Retrieve(ctx, "Which language has good concurrency support and is compiled?")
	if err != nil {
		t.Fatalf("Retrieve error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one retrieved document")
	}

	t.Logf("Retrieved %d documents:", len(docs))
	for i, d := range docs {
		t.Logf("  [%d] source=%s content=%s", i+1, d.Metadata["source"], truncate(d.Content, 80))
	}

	// The top result should be about Go (mentions concurrency and compiled).
	topContent := strings.ToLower(docs[0].Content)
	if !strings.Contains(topContent, "go") && !strings.Contains(topContent, "concurrency") {
		t.Errorf("expected top result to be about Go, got: %s", docs[0].Content)
	}
}

// ---------------------------------------------------------------------------
// Full end-to-end: ingest → agent with RAG → grounded response
// ---------------------------------------------------------------------------

func TestIntegration_RAG_AgentWithRetriever(t *testing.T) {
	embedder := newTestEmbedder(t)
	store := rag.NewMemoryStore()
	provider := newTestProvider(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Ingest some fictional facts the LLM wouldn't know.
	texts := []string{
		"Project Aurora is an internal code name for the company's next-generation billing system, scheduled for Q3 2026.",
		"The Zephyr API uses OAuth2 with PKCE flow and requires the X-Zephyr-Version header set to 2024-01.",
		"Employee onboarding requires completing the InfoSec training module within the first 5 business days.",
	}

	err := rag.Ingest(ctx, store, embedder, texts, nil,
		rag.WithChunkSize(300),
		rag.WithChunkOverlap(0),
	)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}

	retriever := rag.NewRetriever(embedder, store, rag.WithTopK(2))

	a, err := agent.New(provider,
		prompt.Text("You are a helpful internal assistant. Answer questions based only on the provided context. Be brief."),
		nil,
		agent.WithRetriever(retriever),
	)
	if err != nil {
		t.Fatalf("agent creation error: %v", err)
	}

	result, _, err := a.Invoke(ctx, "What is Project Aurora?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Agent response: %s", result)

	lower := strings.ToLower(result)
	if !strings.Contains(lower, "billing") && !strings.Contains(lower, "aurora") {
		t.Errorf("expected response to mention billing or Aurora, got: %s", result)
	}
}

// ---------------------------------------------------------------------------
// Agentic RAG: retriever as a tool
// ---------------------------------------------------------------------------

func TestIntegration_RAG_RetrieverTool(t *testing.T) {
	embedder := newTestEmbedder(t)
	store := rag.NewMemoryStore()
	provider := newTestProvider(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	texts := []string{
		"The cafeteria serves lunch from 11:30 AM to 1:30 PM on weekdays.",
		"Remote work policy allows up to 3 days per week from home.",
		"The annual company retreat is held in September at Lake Tahoe.",
	}

	err := rag.Ingest(ctx, store, embedder, texts, nil,
		rag.WithChunkSize(200),
		rag.WithChunkOverlap(0),
	)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}

	retriever := rag.NewRetriever(embedder, store, rag.WithTopK(2))
	searchTool := agent.NewRetrieverTool("search_docs", "Search internal company documents", retriever)

	a, err := agent.New(provider,
		prompt.Text("You are a company assistant. Use the search_docs tool to find answers. Be brief."),
		[]tool.Tool{searchTool},
	)
	if err != nil {
		t.Fatalf("agent creation error: %v", err)
	}

	result, _, err := a.Invoke(ctx, "When is lunch served?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Agent response: %s", result)

	lower := strings.ToLower(result)
	if !strings.Contains(lower, "11") && !strings.Contains(lower, "lunch") {
		t.Errorf("expected response to mention lunch hours, got: %s", result)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------------------------------------------------------------------------
// Cohere embedder smoke tests
// ---------------------------------------------------------------------------

func TestIntegration_RAG_CohereEmbedEnglishV3(t *testing.T) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "eu-central-1"
	}

	e, err := ragbedrock.CohereEmbedEnglishV3(ragbedrock.WithRegion(region))
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	vec, err := e.Embed(ctx, "The quick brown fox jumps over the lazy dog.")
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if len(vec) == 0 {
		t.Fatal("expected non-empty embedding vector")
	}
	t.Logf("CohereEmbedEnglishV3 dimension: %d", len(vec))

	// Verify semantic similarity: related sentences should score higher than unrelated ones.
	vecA, err := e.Embed(ctx, "I love programming in Go")
	if err != nil {
		t.Fatalf("Embed A: %v", err)
	}
	vecB, err := e.Embed(ctx, "Go is my favourite programming language")
	if err != nil {
		t.Fatalf("Embed B: %v", err)
	}
	vecC, err := e.Embed(ctx, "The weather in Paris is sunny today")
	if err != nil {
		t.Fatalf("Embed C: %v", err)
	}

	simAB := cosine(vecA, vecB)
	simAC := cosine(vecA, vecC)
	t.Logf("sim(Go+Go)=%.4f  sim(Go+weather)=%.4f", simAB, simAC)

	if simAB <= simAC {
		t.Errorf("expected similar sentences to score higher: sim(A,B)=%f <= sim(A,C)=%f", simAB, simAC)
	}
}

func TestIntegration_RAG_CohereEmbedMultilingualV3(t *testing.T) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "eu-central-1"
	}

	e, err := ragbedrock.CohereEmbedMultilingualV3(ragbedrock.WithRegion(region))
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	vec, err := e.Embed(ctx, "The quick brown fox jumps over the lazy dog.")
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if len(vec) == 0 {
		t.Fatal("expected non-empty embedding vector")
	}
	t.Logf("CohereEmbedMultilingualV3 dimension: %d", len(vec))

	// Verify cross-lingual similarity: same sentence in English and German
	// should score higher than an unrelated sentence.
	vecEN, err := e.Embed(ctx, "I love programming in Go")
	if err != nil {
		t.Fatalf("Embed EN: %v", err)
	}
	vecDE, err := e.Embed(ctx, "Ich liebe das Programmieren in Go")
	if err != nil {
		t.Fatalf("Embed DE: %v", err)
	}
	vecUnrelated, err := e.Embed(ctx, "The weather in Paris is sunny today")
	if err != nil {
		t.Fatalf("Embed unrelated: %v", err)
	}

	simCrossLingual := cosine(vecEN, vecDE)
	simUnrelated := cosine(vecEN, vecUnrelated)
	t.Logf("sim(EN+DE same meaning)=%.4f  sim(EN+unrelated)=%.4f", simCrossLingual, simUnrelated)

	if simCrossLingual <= simUnrelated {
		t.Errorf("expected cross-lingual similarity to be higher: sim(EN,DE)=%f <= sim(EN,unrelated)=%f",
			simCrossLingual, simUnrelated)
	}
}

func TestIntegration_RAG_CohereEmbedV4(t *testing.T) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "eu-central-1"
	}

	e, err := ragbedrock.CohereEmbedV4(ragbedrock.WithRegion(region))
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	vec, err := e.Embed(ctx, "The quick brown fox jumps over the lazy dog.")
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if len(vec) == 0 {
		t.Fatal("expected non-empty embedding vector")
	}
	t.Logf("CohereEmbedV4 dimension: %d", len(vec))
}
