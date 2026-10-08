// Package redis provides the Redis lease-only rate-limit backend.
package redis

import (
	"context"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/ratelimit"
	goredis "github.com/redis/go-redis/v9"
)

var _ ratelimit.Store = (*Store)(nil)

// Store holds lease counters and ledgers in one Redis Cluster hash slot.
type Store struct {
	client                  goredis.UniversalClient
	prefix                  string
	pendingTTL, terminalTTL time.Duration
	leaseTTLsConfigured     bool
}
type Option func(*Store)

func WithPrefix(prefix string) Option { return func(s *Store) { s.prefix = prefix } }

// WithLeaseTTLs sets positive minimum pending and terminal ledger retention.
func WithLeaseTTLs(pending, terminal time.Duration) Option {
	return func(s *Store) { s.pendingTTL, s.terminalTTL, s.leaseTTLsConfigured = pending, terminal, true }
}
func NewStore(client goredis.UniversalClient, opts ...Option) *Store {
	s := &Store{client: client, prefix: "ratelimit"}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
func (s *Store) tag() string {
	return strings.NewReplacer("{", "(", "}", ")").Replace(s.prefix) + ":{gude-ratelimit}:lease"
}
func (s *Store) requestKey(key string) string { return s.tag() + ":req:" + key }
func (s *Store) tokenZKey(key string) string  { return s.tag() + ":tok:z:" + key }
func (s *Store) tokenHKey(key string) string  { return s.tag() + ":tok:h:" + key }
func (s *Store) tokenTKey(key string) string  { return s.tag() + ":tok:t:" + key }
func (s *Store) ledgerKey(id string) string   { return s.tag() + ":op:" + id }
func (s *Store) effectivePending(window time.Duration) time.Duration {
	ttl := 2 * window
	if s.pendingTTL > ttl {
		ttl = s.pendingTTL
	}
	return ttl
}
func (s *Store) effectiveTerminal(window time.Duration) time.Duration {
	ttl := window
	if s.terminalTTL > ttl {
		ttl = s.terminalTTL
	}
	return ttl
}
func validTTL(d time.Duration) bool { return d > 0 && d.Milliseconds() > 0 }
func contextFor(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}
