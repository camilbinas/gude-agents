package rag

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// SplitTextE splits text into chunks of at most chunkSize runes with chunkOverlap
// runes of overlap between consecutive chunks. Returns an error for invalid parameters.
// Documented in docs/rag.md — update when changing behavior.
func SplitTextE(text string, chunkSize int, chunkOverlap int) ([]string, error) {
	if chunkSize < 1 {
		return nil, fmt.Errorf("splittext: chunkSize must be >= 1, got %d", chunkSize)
	}
	if chunkOverlap >= chunkSize {
		return nil, fmt.Errorf("splittext: chunkOverlap (%d) must be < chunkSize (%d)", chunkOverlap, chunkSize)
	}

	runes := []rune(text)
	if len(runes) == 0 {
		return []string{}, nil
	}

	var chunks []string
	step := chunkSize - chunkOverlap
	for i := 0; i < len(runes); i += step {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks, nil
}

// SplitText splits text into chunks of at most chunkSize runes with chunkOverlap
// runes of overlap. Invalid parameters are silently clamped.
func SplitText(text string, chunkSize int, chunkOverlap int) []string {
	if chunkSize < 1 {
		chunkSize = 1
	}
	if chunkOverlap < 0 {
		chunkOverlap = 0
	}
	if chunkOverlap >= chunkSize {
		chunkOverlap = chunkSize - 1
	}
	chunks, _ := SplitTextE(text, chunkSize, chunkOverlap)
	return chunks
}

// vsEntry pairs a document with its embedding vector.
type vsEntry struct {
	doc       agent.Document
	embedding []float64
}

// MemoryStore is a brute-force cosine similarity vector store
// backed by a Go slice. Safe for concurrent use.
// Documented in docs/rag.md — update when changing methods or behavior.
type MemoryStore struct {
	mu      sync.RWMutex
	entries []vsEntry
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Add appends documents and their embeddings to the store.
// Returns an error if docs and embeddings have different lengths.
func (s *MemoryStore) Add(ctx context.Context, docs []agent.Document, embeddings [][]float64) error {
	if len(docs) != len(embeddings) {
		return fmt.Errorf("vectorstore: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, doc := range docs {
		s.entries = append(s.entries, vsEntry{doc: doc, embedding: embeddings[i]})
	}
	return nil
}

// Search returns the top-K documents by cosine similarity to queryEmbedding.
// Returns an error if topK < 1. If fewer documents exist than topK, all are returned.
func (s *MemoryStore) Search(ctx context.Context, queryEmbedding []float64, topK int) ([]agent.ScoredDocument, error) {
	if topK < 1 {
		return nil, fmt.Errorf("vectorstore: topK must be >= 1, got %d", topK)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	scored := make([]agent.ScoredDocument, len(s.entries))
	for i, e := range s.entries {
		scored[i] = agent.ScoredDocument{
			Document: e.doc,
			Score:    cosineSimilarity(queryEmbedding, e.embedding),
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	if topK > len(scored) {
		topK = len(scored)
	}
	return scored[:topK], nil
}

// cosineSimilarity computes dot(a,b) / (norm(a) * norm(b)).
func cosineSimilarity(a, b []float64) float64 {
	n := len(a)
	if n != len(b) {
		return 0.0
	}
	var dot, normA, normB float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	magA := math.Sqrt(normA)
	magB := math.Sqrt(normB)
	if magA == 0 || magB == 0 {
		return 0.0
	}
	return dot / (magA * magB)
}

// DefaultContextFormatter is an alias for agent.DefaultContextFormatter.
var DefaultContextFormatter = agent.DefaultContextFormatter

// RetrieverOption configures a Retriever.
type RetrieverOption func(*Retriever)

// Retriever implements agent.Retriever by embedding the query and
// searching a VectorStore for similar documents.
type Retriever struct {
	embedder       agent.Embedder
	store          agent.VectorStore
	topK           int
	scoreThreshold float64
	reranker       agent.Reranker
}

// NewRetriever creates a new Retriever with the given embedder and store.
// Defaults: topK=4, scoreThreshold=0.0, no reranker.
// Documented in docs/rag.md — update when changing defaults or options.
func NewRetriever(embedder agent.Embedder, store agent.VectorStore, opts ...RetrieverOption) *Retriever {
	r := &Retriever{
		embedder:       embedder,
		store:          store,
		topK:           4,
		scoreThreshold: 0.0,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// WithTopK sets the maximum number of documents to retrieve.
func WithTopK(k int) RetrieverOption {
	return func(r *Retriever) { r.topK = k }
}

// WithMaxResults sets the maximum number of documents to retrieve.
// This is an alias for WithTopK.
func WithMaxResults(k int) RetrieverOption { return WithTopK(k) }

// WithScoreThreshold sets the minimum similarity score for returned documents.
func WithScoreThreshold(t float64) RetrieverOption {
	return func(r *Retriever) { r.scoreThreshold = t }
}

// WithReranker attaches a Reranker to the retriever.
func WithReranker(rr agent.Reranker) RetrieverOption {
	return func(r *Retriever) { r.reranker = rr }
}

// Retrieve embeds the query, searches the vector store, filters by score
// threshold, and optionally reranks the results.
func (r *Retriever) Retrieve(ctx context.Context, query string) ([]agent.Document, error) {
	if query == "" {
		return nil, fmt.Errorf("retrieve: query must not be empty")
	}

	embedding, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	scored, err := r.store.Search(ctx, embedding, r.topK)
	if err != nil {
		return nil, err
	}

	var docs []agent.Document
	for _, sd := range scored {
		if sd.Score >= r.scoreThreshold {
			docs = append(docs, sd.Document)
		}
	}

	if r.reranker != nil {
		docs, err = r.reranker.Rerank(ctx, query, docs)
		if err != nil {
			return nil, fmt.Errorf("reranker: %w", err)
		}
	}

	return docs, nil
}

// IngestOption configures the Ingest pipeline.
type IngestOption func(*ingestConfig)

type ingestConfig struct {
	chunkSize    int
	chunkOverlap int
}

// WithChunkSize sets the chunk size for text splitting during ingestion.
func WithChunkSize(n int) IngestOption {
	return func(c *ingestConfig) { c.chunkSize = n }
}

// WithChunkOverlap sets the chunk overlap for text splitting during ingestion.
func WithChunkOverlap(n int) IngestOption {
	return func(c *ingestConfig) { c.chunkOverlap = n }
}

// Ingest splits each text into chunks, embeds each chunk, and stores the
// resulting documents and embeddings in the VectorStore.
// Documented in docs/rag.md — update when changing signature, defaults, or chunking behavior.
func Ingest(
	ctx context.Context,
	store agent.VectorStore,
	embedder agent.Embedder,
	texts []string,
	metadata []map[string]string,
	opts ...IngestOption,
) error {
	cfg := ingestConfig{chunkSize: 512, chunkOverlap: 64}
	for _, opt := range opts {
		opt(&cfg)
	}

	var allDocs []agent.Document
	var allEmbeddings [][]float64

	for si, text := range texts {
		chunks := SplitText(text, cfg.chunkSize, cfg.chunkOverlap)

		var srcMeta map[string]string
		if si < len(metadata) {
			srcMeta = metadata[si]
		}

		for ci, chunk := range chunks {
			merged := make(map[string]string)
			for k, v := range srcMeta {
				merged[k] = v
			}
			merged["source_index"] = strconv.Itoa(si)
			merged["chunk_index"] = strconv.Itoa(ci)

			doc := agent.Document{Content: chunk, Metadata: merged}

			embedding, err := embedder.Embed(ctx, chunk)
			if err != nil {
				return fmt.Errorf("ingest: embed chunk %d: %w", ci, err)
			}

			allDocs = append(allDocs, doc)
			allEmbeddings = append(allEmbeddings, embedding)
		}
	}

	if err := store.Add(ctx, allDocs, allEmbeddings); err != nil {
		return fmt.Errorf("ingest: store.Add: %w", err)
	}

	return nil
}
