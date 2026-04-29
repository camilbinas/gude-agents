package postgres

// Option configures a VectorStore instance.
type Option func(*pgvConfig)

// pgvConfig holds configuration for VectorStore construction.
type pgvConfig struct {
	tableName  string
	colID      string
	colContent string
	colMeta    string
	colEmbed   string
	distMetric string // "cosine", "l2", "inner_product"
}

// WithTableName sets the table name for document storage.
// Default: "documents".
func WithTableName(name string) Option {
	return func(c *pgvConfig) {
		if name != "" {
			c.tableName = name
		}
	}
}

// WithColumns maps the vector store to custom column names on an existing
// table. Pass "" for metadataCol if the table has no metadata column.
func WithColumns(idCol, contentCol, metadataCol, embeddingCol string) Option {
	return func(c *pgvConfig) {
		if idCol != "" {
			c.colID = idCol
		}
		if contentCol != "" {
			c.colContent = contentCol
		}
		c.colMeta = metadataCol
		if embeddingCol != "" {
			c.colEmbed = embeddingCol
		}
	}
}

// WithDistanceMetric sets the distance metric. Supported: "cosine" (default),
// "l2", "inner_product".
func WithDistanceMetric(metric string) Option {
	return func(c *pgvConfig) {
		if metric != "" {
			c.distMetric = metric
		}
	}
}
