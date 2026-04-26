package s3

// S3ConversationOption configures a S3Memory instance.
type S3ConversationOption func(*s3ConversationConfig)

// s3ConversationConfig holds configuration for S3Memory construction.
type s3ConversationConfig struct {
	keyPrefix string
	endpoint  string
	pathStyle bool
}

// WithKeyPrefix sets the key prefix for all S3 object keys. Default: ""
func WithKeyPrefix(prefix string) S3ConversationOption {
	return func(c *s3ConversationConfig) {
		c.keyPrefix = prefix
	}
}

// WithEndpoint sets a custom endpoint URL for S3-compatible providers (MinIO, R2, GCS, etc.).
func WithEndpoint(url string) S3ConversationOption {
	return func(c *s3ConversationConfig) {
		c.endpoint = url
	}
}

// WithPathStyle enables path-style URL addressing (https://host/bucket/key).
// Required by some providers such as MinIO.
func WithPathStyle(enabled bool) S3ConversationOption {
	return func(c *s3ConversationConfig) {
		c.pathStyle = enabled
	}
}
