package dynamodb

// Option configures a DynamoDB Checkpointer instance.
type Option func(*config)

// config holds configuration for DynamoDB Checkpointer construction.
type config struct {
	keyPrefix string
	endpoint  string
}

// WithKeyPrefix sets the key prefix for all DynamoDB partition keys.
// Default: "" (no prefix).
func WithKeyPrefix(prefix string) Option {
	return func(c *config) {
		c.keyPrefix = prefix
	}
}

// WithEndpoint sets a custom endpoint URL for DynamoDB-compatible services
// (e.g. DynamoDB Local at http://localhost:8000). Uses the SDK v2 BaseEndpoint
// option on the DynamoDB client.
func WithEndpoint(url string) Option {
	return func(c *config) {
		c.endpoint = url
	}
}
