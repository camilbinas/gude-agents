package sqlite

import "time"

const defaultTable = "executions"

// Option configures a Store.
type Option func(*config)

type config struct {
	table       string
	busyTimeout time.Duration
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

// WithBusyTimeout sets how long a write waits for a lock held by another
// connection. Default: 5s.
func WithBusyTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.busyTimeout = d
		}
	}
}
