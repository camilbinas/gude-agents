package postgres

// Option configures a PostgresMemory instance.
type Option func(*pgConfig)

// pgConfig holds configuration for PostgresMemory construction.
type pgConfig struct {
	tableName string
}

// WithTableName sets the table name used for conversation storage.
// Default: "conversations".
func WithTableName(name string) Option {
	return func(c *pgConfig) {
		c.tableName = name
	}
}
