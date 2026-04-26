package dynamodb

import "time"

// DynamoDBMemoryOption configures a DynamoDBMemory instance.
type DynamoDBMemoryOption func(*dynamoDBMemoryConfig)

// dynamoDBMemoryConfig holds configuration for DynamoDBMemory construction.
type dynamoDBMemoryConfig struct {
	keyPrefix    string
	ttl          time.Duration
	ttlAttribute string
	pkAttribute  string
	endpoint     string
}

// WithKeyPrefix sets the key prefix for all DynamoDB partition keys. Default: "gude:"
func WithKeyPrefix(prefix string) DynamoDBMemoryOption {
	return func(c *dynamoDBMemoryConfig) {
		c.keyPrefix = prefix
	}
}

// WithTTL sets the TTL for stored conversations. When set, a numeric Unix-epoch
// TTL attribute is written to each DynamoDB item.
func WithTTL(d time.Duration) DynamoDBMemoryOption {
	return func(c *dynamoDBMemoryConfig) {
		c.ttl = d
	}
}

// WithTTLAttribute sets the name of the TTL attribute. Default: "ttl"
func WithTTLAttribute(attr string) DynamoDBMemoryOption {
	return func(c *dynamoDBMemoryConfig) {
		c.ttlAttribute = attr
	}
}

// WithPartitionKey sets the name of the partition key attribute. Default: "conversation_id"
func WithPartitionKey(attr string) DynamoDBMemoryOption {
	return func(c *dynamoDBMemoryConfig) {
		c.pkAttribute = attr
	}
}

// WithEndpoint sets a custom endpoint URL for DynamoDB-compatible services
// (e.g. DynamoDB Local at http://localhost:8000). Uses the SDK v2 BaseEndpoint
// option on the DynamoDB client.
func WithEndpoint(url string) DynamoDBMemoryOption {
	return func(c *dynamoDBMemoryConfig) {
		c.endpoint = url
	}
}
