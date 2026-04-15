// Package redis provides shared Redis connection configuration used by the
// memory and rag Redis drivers.
package redis

import (
	"crypto/tls"

	goredis "github.com/redis/go-redis/v9"
)

// Options holds shared Redis connection configuration.
type Options struct {
	Addr      string // Default: "127.0.0.1:6379"
	Password  string
	DB        int         // Default: 0
	TLSConfig *tls.Config // Optional
}

// NewClient creates a go-redis client from Options, applying defaults.
func NewClient(opts Options) *goredis.Client {
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
