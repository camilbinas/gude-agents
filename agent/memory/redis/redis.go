// Package redis provides a Redis Stack (RediSearch) memory backend
// for use with the gude-agents memory system.
//
// Requires Redis Stack — NOT standard community Redis. The store uses
// RediSearch commands (FT.CREATE, FT.SEARCH) that are only available in
// Redis Stack. Run it locally with:
//
//	docker run -p 6379:6379 redis/redis-stack-server:latest
package redis

import (
	"context"
	"errors"

	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/rag"
	ragredis "github.com/camilbinas/gude-agents/agent/rag/redis"

	"github.com/camilbinas/gude-agents/agent"
)

// Compile-time check: Store implements memory.Memory.
var _ memory.Memory = (*Store)(nil)

// Store implements memory.Memory using Redis Stack (RediSearch)
// for vector similarity search. Internally it delegates to a
// rag/redis.VectorStore wrapped in a rag.ScopedStore and memory.Adapter.
//
// Documented in docs/memory.md
type Store struct {
	adapter *memory.Adapter
	vs      *ragredis.VectorStore
}

// New creates a new Store. It pings Redis to verify connectivity, then creates
// the RediSearch HNSW index via FT.CREATE if it doesn't already exist.
//
// Parameters:
//   - opts: Redis connection configuration
//   - embedder: used to compute embedding vectors for Remember and Recall
//   - dim: embedding dimension (must be >= 1)
//   - sopts: optional functional options (WithIndexName, WithKeyPrefix, etc.)
//
// Returns an error if the embedder is nil, dim < 1, Redis is unreachable,
// or the index cannot be created.
func New(opts Options, embedder agent.Embedder, dim int, sopts ...StoreOption) (*Store, error) {
	if embedder == nil {
		return nil, errors.New("redis memory: embedder is required")
	}
	if dim < 1 {
		return nil, errors.New("redis memory: dim must be at least 1")
	}

	// Apply store options to collect configuration.
	cfg := &storeConfig{
		indexName: "gude_episodic_idx",
		keyPrefix: "gude:episodic:",
		hnswM:     16,
		hnswEF:    200,
	}
	for _, o := range sopts {
		o(cfg)
	}

	// Translate memory/redis.Options to rag/redis.Options.
	ragOpts := ragredis.Options{
		Addr:      opts.Addr,
		Password:  opts.Password,
		DB:        opts.DB,
		TLSConfig: opts.TLSConfig,
	}

	// Translate store options to rag/redis.VectorStoreOption.
	var vopts []ragredis.VectorStoreOption
	if cfg.hnswM != 16 {
		vopts = append(vopts, ragredis.WithHNSWM(cfg.hnswM))
	}
	if cfg.hnswEF != 200 {
		vopts = append(vopts, ragredis.WithHNSWEFConstruction(cfg.hnswEF))
	}
	if cfg.dropExisting {
		vopts = append(vopts, ragredis.WithDropExisting())
	}

	// Create the underlying rag/redis.VectorStore.
	// The indexName is passed directly; the key prefix is indexName + ":".
	vs, err := ragredis.New(ragOpts, cfg.indexName, dim, vopts...)
	if err != nil {
		return nil, err
	}

	// Wrap in ScopedStore and Adapter.
	scopedStore := rag.NewScopedStore(vs)
	adapter := memory.NewAdapter(scopedStore, embedder)

	return &Store{
		adapter: adapter,
		vs:      vs,
	}, nil
}

// Remember stores a fact for the given identifier with an embedding vector
// computed by the configured embedder.
//
// Returns an error if identifier or fact is empty, if the embedder fails,
// or if the Redis HSET command fails.
func (s *Store) Remember(ctx context.Context, identifier, fact string, metadata map[string]string) error {
	return s.adapter.Remember(ctx, identifier, fact, metadata)
}

// Recall retrieves the top entries for the given identifier by semantic
// similarity to the query.
//
// Returns at most limit results ordered by descending similarity score.
// Returns an empty non-nil slice when no entries match.
// Returns an error if identifier is empty, limit < 1, the embedder fails,
// or the FT.SEARCH command fails.
func (s *Store) Recall(ctx context.Context, identifier, query string, limit int) ([]memory.Entry, error) {
	return s.adapter.Recall(ctx, identifier, query, limit)
}

// Close closes the underlying Redis client connection.
func (s *Store) Close() error {
	return s.vs.Close()
}
