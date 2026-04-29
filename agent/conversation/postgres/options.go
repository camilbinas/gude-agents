package postgres

// Option configures a PostgresConversation instance.
type Option func(*pgConfig)

// pgConfig holds configuration for PostgresConversation construction.
type pgConfig struct {
	tableName    string
	colID        string
	colMessages  string
	colUpdatedAt string
}

// defaultConfig returns the default column mapping.
func defaultConfig() *pgConfig {
	return &pgConfig{
		tableName:    "conversations",
		colID:        "conversation_id",
		colMessages:  "messages",
		colUpdatedAt: "updated_at",
	}
}

// WithTableName sets the table name used for conversation storage.
// Default: "conversations".
func WithTableName(name string) Option {
	return func(c *pgConfig) {
		if name != "" {
			c.tableName = name
		}
	}
}

// WithColumns maps the driver to your existing table columns. Pass the column
// names for the conversation ID (TEXT PRIMARY KEY), messages (JSONB), and
// updated-at timestamp (TIMESTAMPTZ). Any empty string keeps the default.
//
// Defaults: "conversation_id", "messages", "updated_at".
func WithColumns(id, messages, updatedAt string) Option {
	return func(c *pgConfig) {
		if id != "" {
			c.colID = id
		}
		if messages != "" {
			c.colMessages = messages
		}
		if updatedAt != "" {
			c.colUpdatedAt = updatedAt
		}
	}
}
