package redis

import (
	"crypto/tls"
)

// Options holds Redis connection configuration.
// Follows the same pattern as agent/rag/redis.Options.
type Options struct {
	Addr      string // Default: "127.0.0.1:6379"
	Password  string
	DB        int         // Default: 0
	TLSConfig *tls.Config // Optional
}

// storeConfig holds the configuration collected from StoreOption functions.
// It is used internally by New to translate options before creating the
// underlying rag/redis.VectorStore.
type storeConfig struct {
	indexName    string
	keyPrefix    string
	hnswM        int
	hnswEF       int
	dropExisting bool
}

// StoreOption configures a Store instance via functional options.
type StoreOption func(*storeConfig)

// WithIndexName sets the RediSearch index name. Default: "gude_episodic_idx".
func WithIndexName(name string) StoreOption {
	return func(c *storeConfig) {
		c.indexName = name
	}
}

// WithKeyPrefix sets the Redis key prefix. Default: "gude:episodic:".
// Note: when wrapping rag/redis.VectorStore, the key prefix is determined
// by the index name (indexName + ":"). This option is kept for backward
// compatibility but the key prefix is now derived from the index name.
func WithKeyPrefix(prefix string) StoreOption {
	return func(c *storeConfig) {
		c.keyPrefix = prefix
	}
}

// WithHNSWM sets the HNSW M parameter. Default: 16.
func WithHNSWM(m int) StoreOption {
	return func(c *storeConfig) {
		c.hnswM = m
	}
}

// WithHNSWEFConstruction sets the HNSW EF_CONSTRUCTION parameter. Default: 200.
func WithHNSWEFConstruction(ef int) StoreOption {
	return func(c *storeConfig) {
		c.hnswEF = ef
	}
}

// WithDropExisting drops the index and its documents before creating a fresh
// one. Useful for examples and development where you want a clean slate on
// every run. Do not use in production — it deletes all indexed data.
func WithDropExisting() StoreOption {
	return func(c *storeConfig) {
		c.dropExisting = true
	}
}
