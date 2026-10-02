package dynamodb

import "time"

type Option func(*config)
type config struct {
	keyPrefix    string
	ttl          time.Duration
	ttlAttribute string
	pkAttribute  string
	skAttribute  string
	endpoint     string
}

func WithKeyPrefix(prefix string) Option  { return func(c *config) { c.keyPrefix = prefix } }
func WithTTL(d time.Duration) Option      { return func(c *config) { c.ttl = d } }
func WithTTLAttribute(attr string) Option { return func(c *config) { c.ttlAttribute = attr } }

// WithPartitionKey sets the HASH key attribute. The append-only layout requires
// a sort key as well; the default partition key is conversation_id.
func WithPartitionKey(attr string) Option { return func(c *config) { c.pkAttribute = attr } }

// WithSortKey sets the RANGE key attribute. New tables must use a string sort
// key; default "sequence" stores META and zero-padded MSG# entries.
func WithSortKey(attr string) Option { return func(c *config) { c.skAttribute = attr } }
func WithEndpoint(url string) Option { return func(c *config) { c.endpoint = url } }
