package redis

import "time"

// Option configures a Store.
type Option func(*config)

type config struct {
	prefix string
	ttl    time.Duration
}

// WithKeyPrefix sets the key prefix. Default: "gude:execution:". An empty
// prefix keeps the default.
func WithKeyPrefix(prefix string) Option {
	return func(c *config) {
		if prefix != "" {
			c.prefix = prefix
		}
	}
}

// WithTTL expires an execution the given duration after its most recent Create
// or Save. Zero, the default, means executions never expire. An expired
// execution is gone: a paused run whose record expired can no longer be
// resumed, so choose a TTL longer than the longest pause you must support.
func WithTTL(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.ttl = d
		}
	}
}
