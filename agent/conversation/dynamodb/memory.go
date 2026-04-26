// Package dynamodb provides a DynamoDB-backed memory driver for the gude-agents framework.
// It stores conversation history as items in an Amazon DynamoDB table.
//
// # Table Schema
//
// The DynamoDB table must be created by the caller with "conversation_id" (String)
// as the partition key and no sort key. Each item stores:
//   - conversation_id: string — keyPrefix + conversationID (configurable via WithPartitionKey)
//   - messages: string — JSON-encoded []agent.Message
//   - ttl: number (optional) — Unix epoch seconds for TTL expiry (configurable via WithTTLAttribute)
//
// # Item Size Limit
//
// DynamoDB items are limited to 400 KB. For long-running conversations that
// accumulate large tool results, pair this driver with conversation.NewWindow or
// conversation.NewSummary to bound item size.
//
// # List Performance
//
// The List method performs a full-table Scan with a filter expression on the
// partition key attribute. This consumes read capacity proportional to the table
// size. Callers with large tables should be aware of the cost and latency
// implications and avoid calling List in hot paths.
package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

// dynamoDBClient is the interface for DynamoDB operations used by DynamoDBMemory.
// The concrete *dynamodb.Client satisfies this interface.
type dynamoDBClient interface {
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// Compile-time interface checks.
var _ agent.Conversation = (*DynamoDBMemory)(nil)
var _ conversation.ConversationManager = (*DynamoDBMemory)(nil)

// DynamoDBMemory implements agent.Conversation and conversation.ConversationManager using Amazon DynamoDB.
type DynamoDBMemory struct {
	client       dynamoDBClient
	table        string
	keyPrefix    string
	ttl          time.Duration
	ttlAttribute string
	pkAttribute  string
}

// NewDynamoDBMemory creates a new DynamoDBMemory. No network calls are made at
// construction time; connectivity errors surface on the first Save/Load call.
//
// Returns an error if table is empty.
func NewDynamoDBMemory(cfg aws.Config, table string, opts ...DynamoDBMemoryOption) (*DynamoDBMemory, error) {
	if table == "" {
		return nil, fmt.Errorf("dynamodb memory: table name is required")
	}

	c := &dynamoDBMemoryConfig{
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}
	for _, o := range opts {
		o(c)
	}

	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if c.endpoint != "" {
			o.BaseEndpoint = aws.String(c.endpoint)
		}
	})

	return &DynamoDBMemory{
		client:       client,
		table:        table,
		keyPrefix:    c.keyPrefix,
		ttl:          c.ttl,
		ttlAttribute: c.ttlAttribute,
		pkAttribute:  c.pkAttribute,
	}, nil
}

// Save persists messages for the given conversation ID as a DynamoDB item.
// When TTL is configured, a numeric Unix-epoch TTL attribute is also written.
func (m *DynamoDBMemory) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return fmt.Errorf("dynamodb memory: save: %w", err)
	}

	pk := m.keyPrefix + conversationID
	item := map[string]dbtypes.AttributeValue{
		m.pkAttribute: &dbtypes.AttributeValueMemberS{Value: pk},
		"messages":    &dbtypes.AttributeValueMemberS{Value: string(data)},
	}

	if m.ttl > 0 {
		ttlVal := time.Now().Add(m.ttl).Unix()
		item[m.ttlAttribute] = &dbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", ttlVal)}
	}

	_, err = m.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(m.table),
		Item:      item,
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) &&
			apiErr.ErrorCode() == "ValidationException" &&
			strings.Contains(apiErr.ErrorMessage(), "Item size has exceeded the maximum allowed size") {
			return fmt.Errorf("dynamodb memory: item too large: conversation exceeds DynamoDB's 400 KB item size limit; consider using conversation.NewWindow or conversation.NewSummary to bound conversation size: %w", err)
		}
		return fmt.Errorf("dynamodb memory: save: %w", err)
	}

	return nil
}

// Load retrieves messages for the given conversation ID.
// Returns an empty non-nil slice if the item does not exist.
func (m *DynamoDBMemory) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	pk := m.keyPrefix + conversationID

	out, err := m.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(m.table),
		Key: map[string]dbtypes.AttributeValue{
			m.pkAttribute: &dbtypes.AttributeValueMemberS{Value: pk},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb memory: load: %w", err)
	}

	if len(out.Item) == 0 {
		return []agent.Message{}, nil
	}

	attr, ok := out.Item["messages"]
	if !ok {
		return []agent.Message{}, nil
	}

	sv, ok := attr.(*dbtypes.AttributeValueMemberS)
	if !ok {
		return nil, fmt.Errorf("dynamodb memory: load: unexpected attribute type for messages")
	}

	messages, err := conversation.UnmarshalMessages([]byte(sv.Value))
	if err != nil {
		return nil, fmt.Errorf("dynamodb memory: load: %w", err)
	}

	return messages, nil
}

// List returns all conversation IDs whose items share the configured key prefix.
// This method performs a full-table Scan — see package documentation for cost implications.
func (m *DynamoDBMemory) List(ctx context.Context) ([]string, error) {
	var ids []string
	var lastKey map[string]dbtypes.AttributeValue

	for {
		out, err := m.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:        aws.String(m.table),
			FilterExpression: aws.String("begins_with(#pk, :prefix)"),
			ExpressionAttributeNames: map[string]string{
				"#pk": m.pkAttribute,
			},
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":prefix": &dbtypes.AttributeValueMemberS{Value: m.keyPrefix},
			},
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("dynamodb memory: list: %w", err)
		}

		for _, item := range out.Items {
			pkAttr, ok := item[m.pkAttribute]
			if !ok {
				continue
			}
			sv, ok := pkAttr.(*dbtypes.AttributeValueMemberS)
			if !ok {
				continue
			}
			id := strings.TrimPrefix(sv.Value, m.keyPrefix)
			ids = append(ids, id)
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	return ids, nil
}

// Delete removes the item for the given conversation ID.
// A not-found response is not treated as an error.
func (m *DynamoDBMemory) Delete(ctx context.Context, conversationID string) error {
	pk := m.keyPrefix + conversationID

	_, err := m.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(m.table),
		Key: map[string]dbtypes.AttributeValue{
			m.pkAttribute: &dbtypes.AttributeValueMemberS{Value: pk},
		},
	})
	if err != nil {
		return fmt.Errorf("dynamodb memory: delete: %w", err)
	}

	return nil
}
