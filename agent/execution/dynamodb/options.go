package dynamodb

// Option configures a Store.
type Option func(*config)

type config struct {
	table     string
	keyPrefix string
}

// WithTableName sets the table that holds executions. An empty name keeps the
// default, "executions".
func WithTableName(name string) Option {
	return func(c *config) {
		if name != "" {
			c.table = name
		}
	}
}

// WithKeyPrefix isolates this store's keys inside a shared table. The prefix is
// length-prefixed internally so overlapping prefixes cannot collide.
// Default: no prefix.
func WithKeyPrefix(prefix string) Option {
	return func(c *config) {
		c.keyPrefix = prefix
	}
}
