// Package redis provides a Redis-backed checkpoint.Checkpointer.
//
// Each thread is stored as one Redis hash. Version allocation, snapshot
// storage, thread-level TTL, and thread registration are committed atomically.
package redis

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/camilbinas/gude-agents/agent/checkpoint"
	goredis "github.com/redis/go-redis/v9"
)

var _ checkpoint.Checkpointer = (*Checkpointer)(nil)

const (
	latestField               = "latest"
	retainIndexedThreadScript = `
if redis.call("EXISTS", KEYS[1]) == 1 then
    return 1
end
redis.call("ZREM", KEYS[2], ARGV[1])
return 0
`
)

type Options struct {
	Addr      string
	Password  string
	DB        int
	TLSConfig *tls.Config
}

type Option func(*config)

type config struct {
	ttl       time.Duration
	keyPrefix string
}

// WithTTL expires a thread and its complete checkpoint history after the
// duration has elapsed since its most recent Save. 0 means no expiration.
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

type Checkpointer struct {
	client    *goredis.Client
	ttl       time.Duration
	keyPrefix string
}

// New creates a Redis checkpointer, pinging Redis to verify connectivity.
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

// Save appends a checkpoint with version allocation, snapshot storage, TTL,
// and thread registration committed in one optimistic transaction.
func (c *Checkpointer) Save(ctx context.Context, threadID string, cp checkpoint.Checkpoint) (checkpoint.Checkpoint, error) {
	if threadID == "" {
		return checkpoint.Checkpoint{}, checkpoint.ErrThreadIDRequired
	}

	threadKey := c.threadKey(threadID)
	for {
		var stored checkpoint.Checkpoint
		err := c.client.Watch(ctx, func(tx *goredis.Tx) error {
			latest := 0
			versionStr, err := tx.HGet(ctx, threadKey, latestField).Result()
			if err != nil && !errors.Is(err, goredis.Nil) {
				return err
			}
			if err == nil {
				latest, err = strconv.Atoi(versionStr)
				if err != nil {
					return fmt.Errorf("parse version: %w", err)
				}
			}

			stored = cp
			stored.ThreadID = threadID
			stored.Version = latest + 1
			if stored.Timestamp.IsZero() {
				stored.Timestamp = time.Now()
			}

			data, err := json.Marshal(stored)
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}

			_, err = tx.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
				pipe.HSet(ctx, threadKey,
					latestField, stored.Version,
					c.versionField(stored.Version), data,
				)
				if c.ttl > 0 {
					pipe.PExpire(ctx, threadKey, c.ttl)
				} else {
					pipe.Persist(ctx, threadKey)
				}
				pipe.ZAdd(ctx, c.threadsKey(), goredis.Z{Score: 0, Member: threadID})
				return nil
			})
			return err
		}, threadKey)

		if err == nil {
			return stored, nil
		}
		if errors.Is(err, goredis.TxFailedErr) {
			if ctx.Err() != nil {
				return checkpoint.Checkpoint{}, ctx.Err()
			}
			continue
		}
		return checkpoint.Checkpoint{}, fmt.Errorf("redis checkpointer: save: %w", err)
	}
}

// Load returns the highest-versioned checkpoint for the thread.
func (c *Checkpointer) Load(ctx context.Context, threadID string) (checkpoint.Checkpoint, error) {
	versionStr, err := c.client.HGet(ctx, c.threadKey(threadID), latestField).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return checkpoint.Checkpoint{}, checkpoint.ErrNotFound
		}
		return checkpoint.Checkpoint{}, fmt.Errorf("redis checkpointer: get latest: %w", err)
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("redis checkpointer: parse version: %w", err)
	}

	return c.loadVersion(ctx, threadID, version)
}

// LoadAt returns the checkpoint at an exact version.
func (c *Checkpointer) LoadAt(ctx context.Context, threadID string, version int) (checkpoint.Checkpoint, error) {
	return c.loadVersion(ctx, threadID, version)
}

// History returns metadata for every checkpoint on the thread, oldest first.
func (c *Checkpointer) History(ctx context.Context, threadID string) ([]checkpoint.Meta, error) {
	threadKey := c.threadKey(threadID)
	versionStr, err := c.client.HGet(ctx, threadKey, latestField).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return []checkpoint.Meta{}, nil
		}
		return nil, fmt.Errorf("redis checkpointer: get latest: %w", err)
	}

	maxVersion, err := strconv.Atoi(versionStr)
	if err != nil {
		return nil, fmt.Errorf("redis checkpointer: parse version: %w", err)
	}
	if maxVersion == 0 {
		return []checkpoint.Meta{}, nil
	}

	fields := make([]string, maxVersion)
	for i := 1; i <= maxVersion; i++ {
		fields[i-1] = c.versionField(i)
	}
	results, err := c.client.HMGet(ctx, threadKey, fields...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis checkpointer: history: %w", err)
	}

	metas := make([]checkpoint.Meta, 0, maxVersion)
	for i, val := range results {
		version := i + 1
		str, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("redis checkpointer: history version %d is missing or not a string", version)
		}
		var cp checkpoint.Checkpoint
		if err := json.Unmarshal([]byte(str), &cp); err != nil {
			return nil, fmt.Errorf("redis checkpointer: history version %d: %w", version, err)
		}
		if cp.Version != version {
			return nil, fmt.Errorf("redis checkpointer: history version %d contains checkpoint version %d", version, cp.Version)
		}
		metas = append(metas, checkpoint.Meta{
			Version:   cp.Version,
			Label:     cp.Label,
			Timestamp: cp.Timestamp,
		})
	}

	return metas, nil
}

// List returns thread IDs whose checkpoint hashes still exist and atomically
// removes index entries left by TTL expiration.
func (c *Checkpointer) List(ctx context.Context) ([]string, error) {
	members, err := c.client.ZRange(ctx, c.threadsKey(), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("redis checkpointer: zrange: %w", err)
	}
	if len(members) == 0 {
		return []string{}, nil
	}

	active := make([]string, 0, len(members))
	for _, threadID := range members {
		exists, err := c.retainIndexedThread(ctx, threadID)
		if err != nil {
			return nil, fmt.Errorf("redis checkpointer: list cleanup: %w", err)
		}
		if exists {
			active = append(active, threadID)
		}
	}
	return active, nil
}

// Delete removes every checkpoint for the thread.
func (c *Checkpointer) Delete(ctx context.Context, threadID string) error {
	_, err := c.client.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.Del(ctx, c.threadKey(threadID))
		pipe.ZRem(ctx, c.threadsKey(), threadID)
		return nil
	})
	if err != nil {
		return fmt.Errorf("redis checkpointer: delete: %w", err)
	}
	return nil
}

func (c *Checkpointer) Close() error {
	return c.client.Close()
}

func (c *Checkpointer) threadKey(threadID string) string {
	return c.keyPrefix + "thread:" + threadID
}

func (c *Checkpointer) versionField(version int) string {
	return "v:" + strconv.Itoa(version)
}

func (c *Checkpointer) threadsKey() string {
	return c.keyPrefix + "threads"
}

func (c *Checkpointer) retainIndexedThread(ctx context.Context, threadID string) (bool, error) {
	result, err := c.client.Eval(ctx, retainIndexedThreadScript,
		[]string{c.threadKey(threadID), c.threadsKey()}, threadID,
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *Checkpointer) loadVersion(ctx context.Context, threadID string, version int) (checkpoint.Checkpoint, error) {
	data, err := c.client.HGet(ctx, c.threadKey(threadID), c.versionField(version)).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return checkpoint.Checkpoint{}, checkpoint.ErrNotFound
		}
		return checkpoint.Checkpoint{}, fmt.Errorf("redis checkpointer: get version: %w", err)
	}

	var cp checkpoint.Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("redis checkpointer: unmarshal: %w", err)
	}
	return cp, nil
}

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
