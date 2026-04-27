package redis

import (
	"crypto/tls"

	goredis "github.com/redis/go-redis/v9"
)

// RedisOptions holds Redis connection configuration.
type RedisOptions struct {
	Addr      string // Default: "127.0.0.1:6379"
	Password  string
	DB        int         // Default: 0
	TLSConfig *tls.Config // Optional
}

// newClient creates a go-redis client from RedisOptions, applying defaults.
func newClient(opts RedisOptions) *goredis.Client {
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
