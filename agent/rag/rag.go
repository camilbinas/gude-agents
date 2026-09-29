// Package rag provides portable retrieval-augmented generation primitives.
package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// Document holds a text chunk and associated metadata.
type Document struct {
	ID       string // Storage-level ID. Empty on input means the store generates one.
	Content  string
	Metadata map[string]string
}

// ScoredDocument pairs a Document with its similarity score.
type ScoredDocument struct {
	Document Document
	Score    float64
}

// Embedder converts text into a float vector.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// Store stores document embeddings and performs similarity search.
type Store interface {
	// Upsert stores documents with their embeddings. If a document's ID is empty,
	// the store generates one. Existing IDs are replaced. Returned IDs follow
	// input order.
	Upsert(ctx context.Context, docs []Document, embeddings [][]float64) (ids []string, err error)

	// Search returns the top-K documents by similarity. Results include IDs.
	Search(ctx context.Context, queryEmbedding []float64, topK int) ([]ScoredDocument, error)

	// Delete removes documents by ID. Missing IDs are ignored.
	Delete(ctx context.Context, ids ...string) error
}

// Manager composes Store with document lookup and metadata-based deletion.
type Manager interface {
	Store

	// Find returns existing documents in input-ID order.
	Find(ctx context.Context, ids ...string) ([]Document, error)

	// DeleteByMetadata deletes documents whose metadata contains every filter
	// entry. Implementations must reject an empty filter.
	DeleteByMetadata(ctx context.Context, filter map[string]string) error
}

// Retriever retrieves relevant documents for a query.
type Retriever interface {
	Retrieve(ctx context.Context, query string) ([]Document, error)
}

// Reranker re-scores a candidate set of documents for a query.
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []Document) ([]Document, error)
}

// FulltextSearcher performs keyword-based document search.
type FulltextSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]ScoredDocument, error)
}

// MetadataFilter represents AND-semantics metadata constraints.
type MetadataFilter map[string]string

// FilteredStore composes Store with metadata-filtered vector search.
type FilteredStore interface {
	Store
	SearchWithFilter(ctx context.Context, queryEmbedding []float64, topK int, filter MetadataFilter) ([]ScoredDocument, error)
}

// FilteredFulltextSearcher composes FulltextSearcher with metadata filtering.
type FilteredFulltextSearcher interface {
	FulltextSearcher
	SearchWithFilter(ctx context.Context, query string, limit int, filter MetadataFilter) ([]ScoredDocument, error)
}

// ContextFormatter formats retrieved documents for prompt injection.
type ContextFormatter func(docs []Document) string

// DefaultContextFormatter formats documents as numbered items wrapped in
// retrieved-context tags so models treat them as external data.
var DefaultContextFormatter ContextFormatter = func(docs []Document) string {
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<retrieved_context>\n")
	for i, doc := range docs {
		fmt.Fprintf(&b, "[%d] %s\n", i+1, doc.Content)
	}
	b.WriteString("</retrieved_context>")
	return b.String()
}

// NewRetrieverTool wraps a Retriever as a tool so the model can decide when
// to retrieve. The first non-nil formatter is used; otherwise the default is
// DefaultContextFormatter.
func NewRetrieverTool(name, description string, r Retriever, formatter ...ContextFormatter) tool.Tool {
	fmtFn := DefaultContextFormatter
	if len(formatter) > 0 && formatter[0] != nil {
		fmtFn = formatter[0]
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []any{"query"},
	}

	return tool.NewRaw(name, description, func(ctx context.Context, input json.RawMessage) (string, error) {
		var params struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return "", err
		}
		docs, err := r.Retrieve(ctx, params.Query)
		if err != nil {
			return "", err
		}
		if len(docs) == 0 {
			return "No relevant documents found.", nil
		}
		return fmtFn(docs), nil
	}, tool.WithSchema(schema))
}

// SplitText splits text into chunks of at most chunkSize runes with overlap
// runes shared by consecutive chunks. chunkSize must be positive and overlap
// must be in [0, chunkSize).
func SplitText(text string, chunkSize, overlap int) ([]string, error) {
	if chunkSize < 1 {
		return nil, fmt.Errorf("splittext: chunkSize must be >= 1, got %d", chunkSize)
	}
	if overlap < 0 {
		return nil, fmt.Errorf("splittext: overlap must be >= 0, got %d", overlap)
	}
	if overlap >= chunkSize {
		return nil, fmt.Errorf("splittext: overlap (%d) must be < chunkSize (%d)", overlap, chunkSize)
	}

	runes := []rune(text)
	if len(runes) == 0 {
		return []string{}, nil
	}

	chunks := make([]string, 0, (len(runes)+chunkSize-1)/chunkSize)
	step := chunkSize - overlap
	for i := 0; i < len(runes); i += step {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks, nil
}

type storeEntry struct {
	id        string
	doc       Document
	embedding []float64
}

// MemoryStore is a brute-force cosine-similarity Store safe for concurrent use.
type MemoryStore struct {
	mu      sync.RWMutex
	entries []storeEntry
	nextID  int
}

var _ Manager = (*MemoryStore)(nil)

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// Upsert implements Store.
func (s *MemoryStore) Upsert(_ context.Context, docs []Document, embeddings [][]float64) ([]string, error) {
	if len(docs) != len(embeddings) {
		return nil, fmt.Errorf("vectorstore: docs and embeddings length mismatch: %d vs %d", len(docs), len(embeddings))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, len(docs))
	for i, doc := range docs {
		id := doc.ID
		if id == "" {
			s.nextID++
			id = strconv.Itoa(s.nextID)
		}
		doc.ID = id
		ids[i] = id
		found := false
		for j, entry := range s.entries {
			if entry.id == id {
				s.entries[j] = storeEntry{id: id, doc: doc, embedding: embeddings[i]}
				found = true
				break
			}
		}
		if !found {
			s.entries = append(s.entries, storeEntry{id: id, doc: doc, embedding: embeddings[i]})
		}
	}
	return ids, nil
}

// Search implements Store.
func (s *MemoryStore) Search(_ context.Context, queryEmbedding []float64, topK int) ([]ScoredDocument, error) {
	if topK < 1 {
		return nil, fmt.Errorf("vectorstore: topK must be >= 1, got %d", topK)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	scored := make([]ScoredDocument, len(s.entries))
	for i, entry := range s.entries {
		scored[i] = ScoredDocument{Document: entry.doc, Score: cosineSimilarity(queryEmbedding, entry.embedding)}
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if topK > len(scored) {
		topK = len(scored)
	}
	return scored[:topK], nil
}

// Find implements Manager.
func (s *MemoryStore) Find(_ context.Context, ids ...string) ([]Document, error) {
	if len(ids) == 0 {
		return []Document{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	byID := make(map[string]Document, len(s.entries))
	for _, entry := range s.entries {
		byID[entry.id] = entry.doc
	}
	result := make([]Document, 0, len(ids))
	for _, id := range ids {
		if doc, ok := byID[id]; ok {
			result = append(result, doc)
		}
	}
	return result, nil
}

// DeleteByMetadata implements Manager.
func (s *MemoryStore) DeleteByMetadata(_ context.Context, filter map[string]string) error {
	if len(filter) == 0 {
		return fmt.Errorf("vectorstore: filter must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.entries[:0]
	for _, entry := range s.entries {
		if !metadataMatchesFilter(entry.doc.Metadata, filter) {
			kept = append(kept, entry)
		}
	}
	s.entries = kept
	return nil
}

func metadataMatchesFilter(metadata map[string]string, filter map[string]string) bool {
	for key, value := range filter {
		if metadata[key] != value {
			return false
		}
	}
	return true
}

// Delete implements Store.
func (s *MemoryStore) Delete(_ context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	remove := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		remove[id] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.entries[:0]
	for _, entry := range s.entries {
		if _, ok := remove[entry.id]; !ok {
			kept = append(kept, entry)
		}
	}
	s.entries = kept
	return nil
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	magA, magB := math.Sqrt(normA), math.Sqrt(normB)
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (magA * magB)
}

// RetrieverOption configures the Retriever returned by NewRetriever.
type RetrieverOption func(*storeRetriever)

type storeRetriever struct {
	embedder       Embedder
	store          Store
	maxResults     int
	scoreThreshold float64
	reranker       Reranker
}

var _ Retriever = (*storeRetriever)(nil)

// NewRetriever creates a Retriever backed by an Embedder and Store.
// It retrieves at most four documents by default.
func NewRetriever(embedder Embedder, store Store, opts ...RetrieverOption) Retriever {
	r := &storeRetriever{embedder: embedder, store: store, maxResults: 4}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// WithMaxResults sets the maximum number of documents to retrieve.
func WithMaxResults(n int) RetrieverOption {
	return func(r *storeRetriever) { r.maxResults = n }
}

// WithScoreThreshold sets the minimum similarity score for returned documents.
func WithScoreThreshold(threshold float64) RetrieverOption {
	return func(r *storeRetriever) { r.scoreThreshold = threshold }
}

// WithReranker attaches a Reranker to the retriever.
func WithReranker(reranker Reranker) RetrieverOption {
	return func(r *storeRetriever) { r.reranker = reranker }
}

func (r *storeRetriever) Retrieve(ctx context.Context, query string) ([]Document, error) {
	if query == "" {
		return nil, fmt.Errorf("retrieve: query must not be empty")
	}
	embedding, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	scored, err := r.store.Search(ctx, embedding, r.maxResults)
	if err != nil {
		return nil, err
	}
	docs := make([]Document, 0, len(scored))
	for _, doc := range scored {
		if doc.Score >= r.scoreThreshold {
			docs = append(docs, doc.Document)
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

// IngestOption configures Ingest.
type IngestOption func(*ingestConfig)

type ingestConfig struct {
	chunkSize    int
	chunkOverlap int
	concurrency  int
}

// WithChunkSize sets the chunk size used during ingestion.
func WithChunkSize(n int) IngestOption { return func(c *ingestConfig) { c.chunkSize = n } }

// WithChunkOverlap sets the chunk overlap used during ingestion.
func WithChunkOverlap(n int) IngestOption { return func(c *ingestConfig) { c.chunkOverlap = n } }

// WithConcurrency sets the maximum number of parallel embedding calls.
func WithConcurrency(n int) IngestOption {
	return func(c *ingestConfig) {
		if n < 1 {
			n = 1
		}
		c.concurrency = n
	}
}

// Ingest splits texts, embeds each chunk, and stores the resulting documents.
func Ingest(ctx context.Context, store Store, embedder Embedder, texts []string, metadata []map[string]string, opts ...IngestOption) error {
	cfg := ingestConfig{chunkSize: 512, chunkOverlap: 64, concurrency: 1}
	for _, opt := range opts {
		opt(&cfg)
	}

	type docChunk struct {
		doc   Document
		chunk string
	}
	var chunks []docChunk
	for sourceIndex, text := range texts {
		parts, err := SplitText(text, cfg.chunkSize, cfg.chunkOverlap)
		if err != nil {
			return fmt.Errorf("ingest: split text %d: %w", sourceIndex, err)
		}
		var sourceMetadata map[string]string
		if sourceIndex < len(metadata) {
			sourceMetadata = metadata[sourceIndex]
		}
		for chunkIndex, part := range parts {
			merged := make(map[string]string, len(sourceMetadata)+2)
			for key, value := range sourceMetadata {
				merged[key] = value
			}
			merged["source_index"] = strconv.Itoa(sourceIndex)
			merged["chunk_index"] = strconv.Itoa(chunkIndex)
			chunks = append(chunks, docChunk{doc: Document{Content: part, Metadata: merged}, chunk: part})
		}
	}
	if len(chunks) == 0 {
		return nil
	}

	docs := make([]Document, len(chunks))
	embeddings := make([][]float64, len(chunks))
	if cfg.concurrency <= 1 {
		for i, chunk := range chunks {
			embedding, err := embedder.Embed(ctx, chunk.chunk)
			if err != nil {
				return fmt.Errorf("ingest: embed chunk %d: %w", i, err)
			}
			docs[i], embeddings[i] = chunk.doc, embedding
		}
	} else {
		sem := make(chan struct{}, cfg.concurrency)
		errs := make([]error, len(chunks))
		var wg sync.WaitGroup
		for i, chunk := range chunks {
			docs[i] = chunk.doc
			wg.Add(1)
			go func(index int, text string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if err := ctx.Err(); err != nil {
					errs[index] = err
					return
				}
				embedding, err := embedder.Embed(ctx, text)
				if err != nil {
					errs[index] = fmt.Errorf("ingest: embed chunk %d: %w", index, err)
					return
				}
				embeddings[index] = embedding
			}(i, chunk.chunk)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
	}

	if manager, ok := store.(Manager); ok {
		sources := make(map[string]struct{})
		for _, item := range metadata {
			if source := item["source"]; source != "" {
				sources[source] = struct{}{}
			}
		}
		for source := range sources {
			if err := manager.DeleteByMetadata(ctx, map[string]string{"source": source}); err != nil {
				return fmt.Errorf("ingest: delete old chunks: %w", err)
			}
		}
	}
	if _, err := store.Upsert(ctx, docs, embeddings); err != nil {
		return fmt.Errorf("ingest: store.Upsert: %w", err)
	}
	return nil
}
