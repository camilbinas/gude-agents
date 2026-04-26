package sqlite

import "time"

// Option configures a SQLiteMemory instance.
type Option func(*sqliteConfig)

// sqliteConfig holds configuration for SQLiteMemory construction.
type sqliteConfig struct {
	tableName   string
	busyTimeout time.Duration
}

// WithTableName sets the table name used for conversation storage.
// Default: "conversations".
func WithTableName(name string) Option {
	return func(c *sqliteConfig) {
		c.tableName = name
	}
}

// WithBusyTimeout sets the SQLite busy timeout for concurrent access.
// Default: 5s.
func WithBusyTimeout(d time.Duration) Option {
	return func(c *sqliteConfig) {
		c.busyTimeout = d
	}
}
