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

	autoMigrate bool
	indexType   string // "hnsw" or "ivfflat"
	hnswM       int
	hnswEFCon   int
	ivfLists    int
	distMetric  string // "cosine", "l2", "inner_product"
}

// WithTableName sets the table name for document storage.
// Default: "documents".
func WithTableName(name string) Option {
	return func(c *pgvConfig) {
		c.tableName = name
	}
}

// WithColumns maps the vector store to custom column names on an existing
// table. This lets you use the vector store with tables like:
//
//	CREATE TABLE users (id TEXT, name TEXT, bio TEXT, embedding vector(1536));
//	store, _ := postgres.New(pool, 1536,
//	    postgres.WithTableName("users"),
//	    postgres.WithColumns("id", "bio", "", "embedding"),
//	)
//
// Parameters: idCol, contentCol, metadataCol, embeddingCol.
// Pass "" for metadataCol if the table has no metadata column — Search will
// return empty metadata and Add will skip writing metadata.
func WithColumns(idCol, contentCol, metadataCol, embeddingCol string) Option {
	return func(c *pgvConfig) {
		c.colID = idCol
		c.colContent = contentCol
		c.colMeta = metadataCol
		c.colEmbed = embeddingCol
	}
}

// WithAutoMigrate enables automatic creation of the pgvector extension,
// table, and index on construction. By default, the table must already exist.
// Use this for development or when the database user has DDL permissions.
func WithAutoMigrate() Option {
	return func(c *pgvConfig) {
		c.autoMigrate = true
	}
}

// WithHNSW configures an HNSW index with the given M and ef_construction
// parameters. Only used with WithAutoMigrate. This is the default index type.
func WithHNSW(m, efConstruction int) Option {
	return func(c *pgvConfig) {
		c.indexType = "hnsw"
		c.hnswM = m
		c.hnswEFCon = efConstruction
	}
}

// WithIVFFlat configures an IVFFlat index with the given number of lists.
// Only used with WithAutoMigrate.
// IVFFlat is faster to build than HNSW but less accurate for small datasets.
func WithIVFFlat(lists int) Option {
	return func(c *pgvConfig) {
		c.indexType = "ivfflat"
		c.ivfLists = lists
	}
}

// WithDistanceMetric sets the distance metric. Supported: "cosine" (default),
// "l2", "inner_product".
func WithDistanceMetric(metric string) Option {
	return func(c *pgvConfig) {
		c.distMetric = metric
	}
}
