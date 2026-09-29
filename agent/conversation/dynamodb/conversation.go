// Package dynamodb provides a DynamoDB-backed conversation store.
//
// The table must use "conversation_id" (String) as its partition key. Each
// item stores JSON messages, a numeric revision, and optionally a TTL value.
package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

type dynamoDBClient interface {
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

var _ agent.ConversationManager = (*Conversation)(nil)

// Conversation implements agent.ConversationManager using DynamoDB.
type Conversation struct {
	client       dynamoDBClient
	table        string
	keyPrefix    string
	ttl          time.Duration
	ttlAttribute string
	pkAttribute  string
}

// New creates a store without making a network request.
func New(cfg aws.Config, table string, opts ...Option) (*Conversation, error) {
	if table == "" {
		return nil, fmt.Errorf("dynamodb conversation: table name is required")
	}
	c := &config{keyPrefix: "gude:", ttlAttribute: "ttl", pkAttribute: "conversation_id"}
	for _, o := range opts {
		o(c)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if c.endpoint != "" {
			o.BaseEndpoint = aws.String(c.endpoint)
		}
	})
	return &Conversation{client: client, table: table, keyPrefix: c.keyPrefix, ttl: c.ttl, ttlAttribute: c.ttlAttribute, pkAttribute: c.pkAttribute}, nil
}

// Save atomically persists messages when expectedRevision matches.
func (m *Conversation) Save(ctx context.Context, conversationID string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	data, err := conversation.MarshalMessages(messages)
	if err != nil {
		return 0, fmt.Errorf("dynamodb conversation: save: %w", err)
	}
	nextRevision := expectedRevision + 1
	item := map[string]dbtypes.AttributeValue{
		m.pkAttribute: &dbtypes.AttributeValueMemberS{Value: m.keyPrefix + conversationID},
		"messages":    &dbtypes.AttributeValueMemberS{Value: string(data)},
		"revision":    &dbtypes.AttributeValueMemberN{Value: strconv.FormatUint(nextRevision, 10)},
	}
	if m.ttl > 0 {
		item[m.ttlAttribute] = &dbtypes.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Add(m.ttl).Unix(), 10)}
	}

	condition := "attribute_not_exists(#pk) OR attribute_not_exists(#revision)"
	names := map[string]string{"#pk": m.pkAttribute, "#revision": "revision"}
	var values map[string]dbtypes.AttributeValue
	if expectedRevision != 0 {
		condition = "#revision = :expected"
		names = map[string]string{"#revision": "revision"}
		values = map[string]dbtypes.AttributeValue{
			":expected": &dbtypes.AttributeValueMemberN{Value: strconv.FormatUint(expectedRevision, 10)},
		}
	}
	_, err = m.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:                 aws.String(m.table),
		Item:                      item,
		ConditionExpression:       aws.String(condition),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "ConditionalCheckFailedException":
				return 0, fmt.Errorf("dynamodb conversation: save %q: %w", conversationID, agent.ErrConversationConflict)
			case "ValidationException":
				if strings.Contains(apiErr.ErrorMessage(), "Item size has exceeded the maximum allowed size") {
					return 0, fmt.Errorf("dynamodb conversation: item too large: conversation exceeds DynamoDB's 400 KB item size limit; consider using conversation.NewWindow or conversation.NewSummary: %w", err)
				}
			}
		}
		return 0, fmt.Errorf("dynamodb conversation: save: %w", err)
	}
	return nextRevision, nil
}

// Load returns a snapshot. Missing items have revision zero and a non-nil
// empty message slice.
func (m *Conversation) Load(ctx context.Context, conversationID string) (agent.ConversationSnapshot, error) {
	out, err := m.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(m.table),
		ConsistentRead: aws.Bool(true),
		Key: map[string]dbtypes.AttributeValue{
			m.pkAttribute: &dbtypes.AttributeValueMemberS{Value: m.keyPrefix + conversationID},
		},
	})
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("dynamodb conversation: load: %w", err)
	}
	if len(out.Item) == 0 {
		return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
	}
	messageAttr, ok := out.Item["messages"].(*dbtypes.AttributeValueMemberS)
	if !ok {
		return agent.ConversationSnapshot{}, fmt.Errorf("dynamodb conversation: load: unexpected attribute type for messages")
	}
	revision := uint64(0)
	if revisionValue, exists := out.Item["revision"]; exists {
		revisionAttr, ok := revisionValue.(*dbtypes.AttributeValueMemberN)
		if !ok {
			return agent.ConversationSnapshot{}, fmt.Errorf("dynamodb conversation: load: unexpected attribute type for revision")
		}
		revision, err = strconv.ParseUint(revisionAttr.Value, 10, 64)
		if err != nil {
			return agent.ConversationSnapshot{}, fmt.Errorf("dynamodb conversation: load revision: %w", err)
		}
	}
	messages, err := conversation.UnmarshalMessages([]byte(messageAttr.Value))
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("dynamodb conversation: load: %w", err)
	}
	return agent.ConversationSnapshot{Messages: messages, Revision: revision}, nil
}

// List returns all IDs sharing the configured prefix.
func (m *Conversation) List(ctx context.Context) ([]string, error) {
	var ids []string
	var lastKey map[string]dbtypes.AttributeValue
	for {
		out, err := m.client.Scan(ctx, &dynamodb.ScanInput{
			TableName: aws.String(m.table), FilterExpression: aws.String("begins_with(#pk, :prefix)"),
			ExpressionAttributeNames:  map[string]string{"#pk": m.pkAttribute},
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{":prefix": &dbtypes.AttributeValueMemberS{Value: m.keyPrefix}},
			ExclusiveStartKey:         lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("dynamodb conversation: list: %w", err)
		}
		for _, item := range out.Items {
			if value, ok := item[m.pkAttribute].(*dbtypes.AttributeValueMemberS); ok {
				ids = append(ids, strings.TrimPrefix(value.Value, m.keyPrefix))
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}
	return ids, nil
}

// Delete removes an item. Missing items are ignored.
func (m *Conversation) Delete(ctx context.Context, conversationID string) error {
	_, err := m.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(m.table),
		Key:       map[string]dbtypes.AttributeValue{m.pkAttribute: &dbtypes.AttributeValueMemberS{Value: m.keyPrefix + conversationID}},
	})
	if err != nil {
		return fmt.Errorf("dynamodb conversation: delete: %w", err)
	}
	return nil
}
