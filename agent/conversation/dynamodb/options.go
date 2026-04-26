package dynamodb

import "time"

// DynamoDBConversationOption configures a DynamoDBMemory instance.
type DynamoDBConversationOption func(*dynamoDBConversationConfig)

// dynamoDBConversationConfig holds configuration for DynamoDBMemory construction.
type dynamoDBConversationConfig struct {
	keyPrefix    string
	ttl          time.Duration
	ttlAttribute string
	pkAttribute  string
	endpoint     string
}

// WithKeyPrefix sets the key prefix for all DynamoDB partition keys. Default: "gude:"
func WithKeyPrefix(prefix string) DynamoDBConversationOption {
	return func(c *dynamoDBConversationConfig) {
		c.keyPrefix = prefix
	}
}

// WithTTL sets the TTL for stored conversations. When set, a numeric Unix-epoch
// TTL attribute is written to each DynamoDB item.
func WithTTL(d time.Duration) DynamoDBConversationOption {
	return func(c *dynamoDBConversationConfig) {
		c.ttl = d
	}
}

// WithTTLAttribute sets the name of the TTL attribute. Default: "ttl"
func WithTTLAttribute(attr string) DynamoDBConversationOption {
	return func(c *dynamoDBConversationConfig) {
		c.ttlAttribute = attr
	}
}

// WithPartitionKey sets the name of the partition key attribute. Default: "conversation_id"
func WithPartitionKey(attr string) DynamoDBConversationOption {
	return func(c *dynamoDBConversationConfig) {
		c.pkAttribute = attr
	}
}

// WithEndpoint sets a custom endpoint URL for DynamoDB-compatible services
// (e.g. DynamoDB Local at http://localhost:8000). Uses the SDK v2 BaseEndpoint
// option on the DynamoDB client.
func WithEndpoint(url string) DynamoDBConversationOption {
	return func(c *dynamoDBConversationConfig) {
		c.endpoint = url
	}
}
