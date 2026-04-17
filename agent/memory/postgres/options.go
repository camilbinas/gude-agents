package postgres

// Option configures a PostgresMemory instance.
type Option func(*pgConfig)

// pgConfig holds configuration for PostgresMemory construction.
type pgConfig struct {
	tableName   string
	autoMigrate bool
}

// WithTableName sets the table name used for conversation storage.
// Default: "conversations".
func WithTableName(name string) Option {
	return func(c *pgConfig) {
		c.tableName = name
	}
}

// WithAutoMigrate enables automatic table creation on construction.
// By default, the table must already exist. Use this for development
// or when the database user has CREATE TABLE permissions.
//
// The created table has the schema:
//
//	CREATE TABLE IF NOT EXISTS <table> (
//	    conversation_id TEXT PRIMARY KEY,
//	    messages        JSONB NOT NULL,
//	    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	)
func WithAutoMigrate() Option {
	return func(c *pgConfig) {
		c.autoMigrate = true
	}
}
