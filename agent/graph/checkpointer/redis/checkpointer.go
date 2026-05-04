// Package redis provides a Redis-backed implementation of the GraphCheckpointer
// interface for persistent graph execution checkpointing.
package redis

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/camilbinas/gude-agents/agent/graph"
	goredis "github.com/redis/go-redis/v9"
)

// Compile-time interface check.
var _ graph.GraphCheckpointer = (*Checkpointer)(nil)

// Options holds Redis connection configuration.
type Options struct {
	Addr      string // Default: "127.0.0.1:6379"
	Password  string
	DB        int         // Default: 0
	TLSConfig *tls.Config // Optional
}

// Option configures a Checkpointer instance.
type Option func(*config)

type config struct {
	ttl       time.Duration
	keyPrefix string
}

// WithTTL sets the TTL for checkpoint keys. 0 means no expiration.
func WithTTL(d time.Duration) Option {
	return func(c *config) {
		c.ttl = d
	}
}

// WithKeyPrefix sets the key prefix. Default: "gude:checkpoint:"
func WithKeyPrefix(prefix string) Option {
	return func(c *config) {
		if prefix != "" {
			c.keyPrefix = prefix
		}
	}
}

// Checkpointer implements graph.GraphCheckpointer using Redis.
type Checkpointer struct {
	client    *goredis.Client
	ttl       time.Duration
	keyPrefix string
}

// New creates a new Redis checkpointer. Pings Redis to verify connectivity.
func New(opts Options, mopts ...Option) (*Checkpointer, error) {
	cfg := &config{
		ttl:       0,
		keyPrefix: "gude:checkpoint:",
	}
	for _, o := range mopts {
		o(cfg)
	}

	client := newClient(opts)

	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis checkpointer: ping: %w", err)
	}

	return &Checkpointer{
		client:    client,
		ttl:       cfg.ttl,
		keyPrefix: cfg.keyPrefix,
	}, nil
}

// Save persists a checkpoint for the given thread. It atomically increments
// the version counter and stores the checkpoint as JSON.
func (c *Checkpointer) Save(ctx context.Context, threadID string, cp graph.Checkpoint) (graph.Checkpoint, error) {
	latestKey := c.latestKey(threadID)

	// Atomically increment version.
	version, err := c.client.Incr(ctx, latestKey).Result()
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("redis checkpointer: incr version: %w", err)
	}

	cp.ThreadID = threadID
	cp.Version = int(version)
	if cp.Timestamp.IsZero() {
		cp.Timestamp = time.Now()
	}

	data, err := json.Marshal(cp)
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("redis checkpointer: marshal: %w", err)
	}

	versionKey := c.versionKey(threadID, int(version))
	if err := c.client.Set(ctx, versionKey, data, c.ttl).Err(); err != nil {
		return graph.Checkpoint{}, fmt.Errorf("redis checkpointer: set: %w", err)
	}

	// Set TTL on the latest key as well if configured.
	if c.ttl > 0 {
		c.client.Expire(ctx, latestKey, c.ttl)
	}

	// Track thread ID in sorted set.
	if err := c.client.ZAdd(ctx, c.threadsKey(), goredis.Z{Score: 0, Member: threadID}).Err(); err != nil {
		return graph.Checkpoint{}, fmt.Errorf("redis checkpointer: zadd: %w", err)
	}

	return cp, nil
}

// Load retrieves the latest checkpoint for the given thread.
// Returns graph.ErrCheckpointNotFound if no checkpoints exist.
func (c *Checkpointer) Load(ctx context.Context, threadID string) (graph.Checkpoint, error) {
	latestKey := c.latestKey(threadID)

	versionStr, err := c.client.Get(ctx, latestKey).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return graph.Checkpoint{}, graph.ErrCheckpointNotFound
		}
		return graph.Checkpoint{}, fmt.Errorf("redis checkpointer: get latest: %w", err)
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("redis checkpointer: parse version: %w", err)
	}

	return c.loadVersion(ctx, threadID, version)
}

// LoadAt retrieves the checkpoint at a specific version.
// Returns graph.ErrCheckpointNotFound if the version does not exist.
func (c *Checkpointer) LoadAt(ctx context.Context, threadID string, version int) (graph.Checkpoint, error) {
	return c.loadVersion(ctx, threadID, version)
}

// History returns ordered checkpoint metadata for a thread (oldest first).
func (c *Checkpointer) History(ctx context.Context, threadID string) ([]graph.CheckpointMeta, error) {
	latestKey := c.latestKey(threadID)

	versionStr, err := c.client.Get(ctx, latestKey).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return []graph.CheckpointMeta{}, nil
		}
		return nil, fmt.Errorf("redis checkpointer: get latest: %w", err)
	}

	maxVersion, err := strconv.Atoi(versionStr)
	if err != nil {
		return nil, fmt.Errorf("redis checkpointer: parse version: %w", err)
	}

	if maxVersion == 0 {
		return []graph.CheckpointMeta{}, nil
	}

	// Build keys for MGET.
	keys := make([]string, maxVersion)
	for i := 1; i <= maxVersion; i++ {
		keys[i-1] = c.versionKey(threadID, i)
	}

	results, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis checkpointer: mget: %w", err)
	}

	metas := make([]graph.CheckpointMeta, 0, maxVersion)
	for _, val := range results {
		if val == nil {
			continue
		}
		str, ok := val.(string)
		if !ok {
			continue
		}
		var cp graph.Checkpoint
		if err := json.Unmarshal([]byte(str), &cp); err != nil {
			continue
		}
		metas = append(metas, graph.CheckpointMeta{
			Version:   cp.Version,
			NodeName:  cp.NodeName,
			Timestamp: cp.Timestamp,
		})
	}

	return metas, nil
}

// List returns all thread IDs that have stored checkpoints.
func (c *Checkpointer) List(ctx context.Context) ([]string, error) {
	members, err := c.client.ZRange(ctx, c.threadsKey(), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("redis checkpointer: zrange: %w", err)
	}
	return members, nil
}

// Delete removes all checkpoints for a thread.
func (c *Checkpointer) Delete(ctx context.Context, threadID string) error {
	latestKey := c.latestKey(threadID)

	versionStr, err := c.client.Get(ctx, latestKey).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil // Nothing to delete.
		}
		return fmt.Errorf("redis checkpointer: get latest: %w", err)
	}

	maxVersion, err := strconv.Atoi(versionStr)
	if err != nil {
		return fmt.Errorf("redis checkpointer: parse version: %w", err)
	}

	// Collect all keys to delete.
	keys := make([]string, 0, maxVersion+1)
	keys = append(keys, latestKey)
	for i := 1; i <= maxVersion; i++ {
		keys = append(keys, c.versionKey(threadID, i))
	}

	if err := c.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis checkpointer: del: %w", err)
	}

	// Remove from threads set.
	if err := c.client.ZRem(ctx, c.threadsKey(), threadID).Err(); err != nil {
		return fmt.Errorf("redis checkpointer: zrem: %w", err)
	}

	return nil
}

// Close closes the underlying Redis client.
func (c *Checkpointer) Close() error {
	return c.client.Close()
}

// --- internal helpers ---

func (c *Checkpointer) latestKey(threadID string) string {
	return c.keyPrefix + "thread:" + threadID + ":latest"
}

func (c *Checkpointer) versionKey(threadID string, version int) string {
	return c.keyPrefix + "thread:" + threadID + ":v:" + strconv.Itoa(version)
}

func (c *Checkpointer) threadsKey() string {
	return c.keyPrefix + "threads"
}

func (c *Checkpointer) loadVersion(ctx context.Context, threadID string, version int) (graph.Checkpoint, error) {
	versionKey := c.versionKey(threadID, version)

	data, err := c.client.Get(ctx, versionKey).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return graph.Checkpoint{}, graph.ErrCheckpointNotFound
		}
		return graph.Checkpoint{}, fmt.Errorf("redis checkpointer: get version: %w", err)
	}

	var cp graph.Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return graph.Checkpoint{}, fmt.Errorf("redis checkpointer: unmarshal: %w", err)
	}

	return cp, nil
}

// newClient creates a go-redis client from Options, applying defaults.
func newClient(opts Options) *goredis.Client {
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	return goredis.NewClient(&goredis.Options{
		Addr:      addr,
		Password:  opts.Password,
		DB:        opts.DB,
		TLSConfig: opts.TLSConfig,
	})
}
