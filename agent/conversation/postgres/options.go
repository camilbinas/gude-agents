package postgres

// Option configures a Conversation.
type Option func(*pgConfig)

type pgConfig struct {
	tableName    string
	messageTable string
	colID        string
	colMessages  string // legacy snapshot column, migration-only
	colRevision  string
	colUpdatedAt string
}

func defaultConfig() *pgConfig {
	return &pgConfig{
		tableName:    "conversations",
		messageTable: "conversation_messages",
		colID:        "conversation_id",
		colMessages:  "messages",
		colRevision:  "revision",
		colUpdatedAt: "updated_at",
	}
}

// WithTableName sets the storage table name.
func WithTableName(name string) Option {
	return func(c *pgConfig) {
		if name != "" {
			c.tableName = name
			c.messageTable = name + "_messages"
		}
	}
}

// WithColumns maps the ID, messages, and updated-at columns. Empty values keep
// their defaults. Configure the revision column separately with WithRevisionColumn.
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

// WithRevisionColumn sets the BIGINT revision column used for compare-and-swap.
func WithRevisionColumn(revision string) Option {
	return func(c *pgConfig) {
		if revision != "" {
			c.colRevision = revision
		}
	}
}
