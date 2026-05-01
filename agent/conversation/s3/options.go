package s3

// Option configures a S3Memory instance.
type Option func(*config)

// config holds configuration for S3Memory construction.
type config struct {
	keyPrefix string
	endpoint  string
	pathStyle bool
}

// WithKeyPrefix sets the key prefix for all S3 object keys. Default: ""
func WithKeyPrefix(prefix string) Option {
	return func(c *config) {
		c.keyPrefix = prefix
	}
}

// WithEndpoint sets a custom endpoint URL for S3-compatible providers (MinIO, R2, GCS, etc.).
func WithEndpoint(url string) Option {
	return func(c *config) {
		c.endpoint = url
	}
}

// WithPathStyle enables path-style URL addressing (https://host/bucket/key).
// Required by some providers such as MinIO.
func WithPathStyle(enabled bool) Option {
	return func(c *config) {
		c.pathStyle = enabled
	}
}
