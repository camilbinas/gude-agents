package postgres

// storeConfig holds the configuration collected from StoreOption functions.
type storeConfig struct {
	tableName    string
	autoMigrate  bool
	hnswM        int
	hnswEFCon    int
	ivfLists     int
	distMetric   string
	dropExisting bool
}

// StoreOption configures a Store instance via functional options.
type StoreOption func(*storeConfig)

// WithTableName sets the PostgreSQL table name. Default: "memory_entries".
func WithTableName(name string) StoreOption {
	return func(c *storeConfig) {
		c.tableName = name
	}
}

// WithAutoMigrate enables automatic creation of the pgvector extension,
// table, and indexes on construction. By default, the table must already exist.
func WithAutoMigrate() StoreOption {
	return func(c *storeConfig) {
		c.autoMigrate = true
	}
}

// WithHNSW configures an HNSW index with the given M and ef_construction
// parameters. Only used with WithAutoMigrate. This is the default index type.
func WithHNSW(m, efConstruction int) StoreOption {
	return func(c *storeConfig) {
		c.hnswM = m
		c.hnswEFCon = efConstruction
	}
}

// WithIVFFlat configures an IVFFlat index with the given number of lists.
// Only used with WithAutoMigrate.
func WithIVFFlat(lists int) StoreOption {
	return func(c *storeConfig) {
		c.ivfLists = lists
	}
}

// WithDistanceMetric sets the distance metric. Supported: "cosine" (default),
// "l2", "inner_product".
func WithDistanceMetric(metric string) StoreOption {
	return func(c *storeConfig) {
		c.distMetric = metric
	}
}

// WithDropExisting drops and recreates the table before creating the
// store. Useful for examples and development where you want a clean
// slate on every run. Do not use in production — it deletes all stored data.
func WithDropExisting() StoreOption {
	return func(c *storeConfig) {
		c.dropExisting = true
	}
}
