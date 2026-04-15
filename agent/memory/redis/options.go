package redis

import (
	baseredis "github.com/camilbinas/gude-agents/agent/redis"
	goredis "github.com/redis/go-redis/v9"
)

// RedisOptions holds shared Redis connection configuration.
// Documented in docs/redis.md — update when changing fields.
type RedisOptions = baseredis.Options

// newClient creates a go-redis client from RedisOptions, applying defaults.
func newClient(opts RedisOptions) *goredis.Client {
	return baseredis.NewClient(opts)
}
