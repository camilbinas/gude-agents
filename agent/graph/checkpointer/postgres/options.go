package postgres

// Option configures a Postgres Checkpointer instance.
type Option func(*pgConfig)

// pgConfig holds configuration for Postgres Checkpointer construction.
type pgConfig struct {
	tableName string
}

// defaultConfig returns the default configuration.
func defaultConfig() *pgConfig {
	return &pgConfig{
		tableName: "graph_checkpoints",
	}
}

// WithTableName sets the table name used for checkpoint storage.
// Default: "graph_checkpoints".
func WithTableName(name string) Option {
	return func(c *pgConfig) {
		if name != "" {
			c.tableName = name
		}
	}
}
