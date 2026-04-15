package s3

// S3MemoryOption configures a S3Memory instance.
type S3MemoryOption func(*blobMemoryConfig)

// blobMemoryConfig holds configuration for S3Memory construction.
type blobMemoryConfig struct {
	keyPrefix string
	endpoint  string
	pathStyle bool
}

// WithKeyPrefix sets the key prefix for all S3 object keys. Default: ""
func WithKeyPrefix(prefix string) S3MemoryOption {
	return func(c *blobMemoryConfig) {
		c.keyPrefix = prefix
	}
}

// WithEndpoint sets a custom endpoint URL for S3-compatible providers (MinIO, R2, GCS, etc.).
func WithEndpoint(url string) S3MemoryOption {
	return func(c *blobMemoryConfig) {
		c.endpoint = url
	}
}

// WithPathStyle enables path-style URL addressing (https://host/bucket/key).
// Required by some providers such as MinIO.
func WithPathStyle(enabled bool) S3MemoryOption {
	return func(c *blobMemoryConfig) {
		c.pathStyle = enabled
	}
}
