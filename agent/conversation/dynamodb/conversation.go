// Package dynamodb provides an append-only DynamoDB conversation store.
//
// The table must have a string HASH key (conversation_id by default) and a
// string RANGE key (sequence by default). Each conversation uses one partition:
// conv#<id>/META stores revisions/state and MSG#000000000001... store events.
package dynamodb

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	BatchWriteItem(context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

var (
	_ agent.ConversationManager = (*Conversation)(nil)
	_ agent.ContextStateStore   = (*Conversation)(nil)
)

type Conversation struct {
	client                                 dynamoDBClient
	table, keyPrefix                       string
	ttl                                    time.Duration
	ttlAttribute, pkAttribute, skAttribute string
	waitBatchWriteRetry                    func(context.Context, int) error
}

func New(cfg aws.Config, table string, opts ...Option) (*Conversation, error) {
	if table == "" {
		return nil, fmt.Errorf("dynamodb conversation: table name is required")
	}
	c := &config{keyPrefix: "gude:", ttlAttribute: "ttl", pkAttribute: "conversation_id", skAttribute: "sequence"}
	for _, o := range opts {
		o(c)
	}
	if c.pkAttribute == "" || c.skAttribute == "" {
		return nil, errors.New("dynamodb conversation: partition and sort key attributes are required")
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if c.endpoint != "" {
			o.BaseEndpoint = aws.String(c.endpoint)
		}
	})
	return &Conversation{client: client, table: table, keyPrefix: c.keyPrefix, ttl: c.ttl, ttlAttribute: c.ttlAttribute, pkAttribute: c.pkAttribute, skAttribute: c.skAttribute, waitBatchWriteRetry: waitBatchWriteRetry}, nil
}
func (m *Conversation) partition(id string) string { return m.keyPrefix + "conv#" + id }
func msgKey(sequence uint64) string                { return fmt.Sprintf("MSG#%020d", sequence) }
func (m *Conversation) key(id, sk string) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{m.pkAttribute: &dbtypes.AttributeValueMemberS{Value: m.partition(id)}, m.skAttribute: &dbtypes.AttributeValueMemberS{Value: sk}}
}
func avs(v string) dbtypes.AttributeValue { return &dbtypes.AttributeValueMemberS{Value: v} }
func avn(v uint64) dbtypes.AttributeValue {
	return &dbtypes.AttributeValueMemberN{Value: strconv.FormatUint(v, 10)}
}
func isConflict(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && (api.ErrorCode() == "ConditionalCheckFailedException" || api.ErrorCode() == "TransactionCanceledException")
}
func isMissing(err error) bool { return errors.Is(err, context.Canceled) }
func (m *Conversation) Load(ctx context.Context, id string) (agent.ConversationSnapshot, error) {
	return m.LoadAfter(ctx, id, 0)
}
func (m *Conversation) LoadAfter(ctx context.Context, id string, after uint64) (agent.ConversationSnapshot, error) {
	meta, err := m.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(m.table), ConsistentRead: aws.Bool(true), Key: m.key(id, "META")})
	if err != nil {
		return agent.ConversationSnapshot{}, fmt.Errorf("dynamodb conversation: load metadata: %w", err)
	}
	if len(meta.Item) == 0 {
		return agent.ConversationSnapshot{Messages: []agent.Message{}}, nil
	}
	rev, last, err := metadata(meta.Item)
	if err != nil {
		return agent.ConversationSnapshot{}, err
	}
	msgs := []agent.Message{}
	var start map[string]dbtypes.AttributeValue
	for {
		if err := ctx.Err(); err != nil {
			return agent.ConversationSnapshot{}, err
		}
		out, err := m.client.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(m.table), ConsistentRead: aws.Bool(true), KeyConditionExpression: aws.String("#pk = :pk AND #sk > :after"), ExpressionAttributeNames: map[string]string{"#pk": m.pkAttribute, "#sk": m.skAttribute}, ExpressionAttributeValues: map[string]dbtypes.AttributeValue{":pk": avs(m.partition(id)), ":after": avs(msgKey(after))}, ExclusiveStartKey: start})
		if err != nil {
			return agent.ConversationSnapshot{}, fmt.Errorf("dynamodb conversation: load range: %w", err)
		}
		for _, item := range out.Items {
			if sk, ok := item[m.skAttribute].(*dbtypes.AttributeValueMemberS); !ok || !strings.HasPrefix(sk.Value, "MSG#") {
				continue
			}
			raw, ok := item["message"].(*dbtypes.AttributeValueMemberS)
			if !ok {
				return agent.ConversationSnapshot{}, errors.New("dynamodb conversation: message field missing")
			}
			one, err := conversation.UnmarshalMessages([]byte(raw.Value))
			if err != nil || len(one) != 1 {
				if err == nil {
					err = errors.New("expected one message")
				}
				return agent.ConversationSnapshot{}, fmt.Errorf("dynamodb conversation: decode event: %w", err)
			}
			msgs = append(msgs, one[0])
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	return agent.ConversationSnapshot{Messages: msgs, Revision: rev, LastSequence: last}, nil
}
func metadata(item map[string]dbtypes.AttributeValue) (uint64, uint64, error) {
	num := func(name string) (uint64, error) {
		v, ok := item[name].(*dbtypes.AttributeValueMemberN)
		if !ok {
			return 0, nil
		}
		return strconv.ParseUint(v.Value, 10, 64)
	}
	rev, err := num("revision")
	if err != nil {
		return 0, 0, err
	}
	last, err := num("last_sequence")
	return rev, last, err
}
func (m *Conversation) Append(ctx context.Context, id string, msgs []agent.Message, expected uint64) (agent.ConversationCursor, error) {
	if len(msgs) > 24 {
		return agent.ConversationCursor{}, fmt.Errorf("dynamodb conversation: append batch of %d exceeds transactional limit of 24 messages", len(msgs))
	}
	meta, err := m.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(m.table), ConsistentRead: aws.Bool(true), Key: m.key(id, "META")})
	if err != nil {
		return agent.ConversationCursor{}, fmt.Errorf("dynamodb conversation: append read metadata: %w", err)
	}
	var rev, last uint64
	if len(meta.Item) > 0 {
		rev, last, err = metadata(meta.Item)
		if err != nil {
			return agent.ConversationCursor{}, err
		}
	}
	if rev != expected {
		return agent.ConversationCursor{}, fmt.Errorf("dynamodb conversation: append %q: %w", id, agent.ErrConversationConflict)
	}
	if len(msgs) == 0 {
		return agent.ConversationCursor{Revision: rev, LastSequence: last}, nil
	}
	next, nextLast := rev+1, last+uint64(len(msgs))
	names := map[string]string{"#revision": "revision", "#last": "last_sequence", "#pk": m.pkAttribute}
	values := map[string]dbtypes.AttributeValue{":expected": avn(expected), ":next": avn(next), ":last": avn(nextLast), ":zero": avn(0)}
	update := &dbtypes.Update{TableName: aws.String(m.table), Key: m.key(id, "META"), UpdateExpression: aws.String("SET #revision = :next, #last = :last"), ConditionExpression: aws.String("attribute_not_exists(#pk) OR #revision = :expected"), ExpressionAttributeNames: names, ExpressionAttributeValues: values}
	if m.ttl > 0 {
		update.UpdateExpression = aws.String("SET #revision = :next, #last = :last, #ttl = :ttl")
		update.ExpressionAttributeNames["#ttl"] = m.ttlAttribute
		update.ExpressionAttributeValues[":ttl"] = &dbtypes.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Add(m.ttl).Unix(), 10)}
	}
	items := make([]dbtypes.TransactWriteItem, 0, len(msgs)+1)
	items = append(items, dbtypes.TransactWriteItem{Update: update})
	for i, msg := range msgs {
		raw, err := conversation.MarshalMessages([]agent.Message{msg})
		if err != nil {
			return agent.ConversationCursor{}, fmt.Errorf("dynamodb conversation: marshal message: %w", err)
		}
		item := m.key(id, msgKey(last+uint64(i)+1))
		item["message"] = avs(string(raw))
		if m.ttl > 0 {
			item[m.ttlAttribute] = &dbtypes.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Add(m.ttl).Unix(), 10)}
		}
		items = append(items, dbtypes.TransactWriteItem{Put: &dbtypes.Put{TableName: aws.String(m.table), Item: item, ConditionExpression: aws.String("attribute_not_exists(#pk)"), ExpressionAttributeNames: map[string]string{"#pk": m.pkAttribute}}})
	}
	_, err = m.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if err != nil {
		if isConflict(err) {
			return agent.ConversationCursor{}, fmt.Errorf("dynamodb conversation: append %q: %w", id, agent.ErrConversationConflict)
		}
		return agent.ConversationCursor{}, fmt.Errorf("dynamodb conversation: append: %w", err)
	}
	return agent.ConversationCursor{Revision: next, LastSequence: nextLast}, nil
}
func (m *Conversation) stateAttr(key string) (string, string) {
	e := base64.RawURLEncoding.EncodeToString([]byte(key))
	return "context_" + e, "context_revision_" + e
}
func (m *Conversation) LoadContextState(ctx context.Context, id, key string) (agent.ContextStateSnapshot, error) {
	field, revField := m.stateAttr(key)
	out, err := m.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(m.table), ConsistentRead: aws.Bool(true), Key: m.key(id, "META")})
	if err != nil {
		return agent.ContextStateSnapshot{}, fmt.Errorf("dynamodb conversation: load context state: %w", err)
	}
	if len(out.Item) == 0 {
		return agent.ContextStateSnapshot{}, nil
	}
	raw, ok := out.Item[field].(*dbtypes.AttributeValueMemberS)
	if !ok {
		return agent.ContextStateSnapshot{}, nil
	}
	rev := uint64(0)
	if v, ok := out.Item[revField].(*dbtypes.AttributeValueMemberN); ok {
		rev, _ = strconv.ParseUint(v.Value, 10, 64)
	}
	return agent.ContextStateSnapshot{Data: json.RawMessage(raw.Value), Revision: rev}, nil
}
func (m *Conversation) SaveContextState(ctx context.Context, id, key string, data json.RawMessage, expected uint64) (uint64, error) {
	if !json.Valid(data) {
		return 0, fmt.Errorf("dynamodb conversation: context state %q is invalid JSON", key)
	}
	field, revField := m.stateAttr(key)
	current, err := m.LoadContextState(ctx, id, key)
	if err != nil {
		return 0, err
	}
	if current.Revision != expected {
		return 0, fmt.Errorf("dynamodb conversation: context state %q: %w", key, agent.ErrContextStateConflict)
	}
	next := expected + 1
	names := map[string]string{"#f": field, "#r": revField}
	values := map[string]dbtypes.AttributeValue{":data": avs(string(data)), ":next": avn(next), ":expected": avn(expected), ":zero": avn(0)}
	condition := "attribute_not_exists(#r) OR #r = :expected"
	update := "SET #f = :data, #r = :next"
	if m.ttl > 0 {
		names["#ttl"] = m.ttlAttribute
		values[":ttl"] = &dbtypes.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Add(m.ttl).Unix(), 10)}
		update += ", #ttl = :ttl"
	}
	_, err = m.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(m.table), Key: m.key(id, "META"), UpdateExpression: aws.String(update), ConditionExpression: aws.String(condition), ExpressionAttributeNames: names, ExpressionAttributeValues: values})
	if err != nil {
		if isConflict(err) {
			return 0, fmt.Errorf("dynamodb conversation: context state %q: %w", key, agent.ErrContextStateConflict)
		}
		return 0, fmt.Errorf("dynamodb conversation: save context state: %w", err)
	}
	return next, nil
}
func (m *Conversation) List(ctx context.Context) ([]string, error) {
	var ids []string
	var start map[string]dbtypes.AttributeValue
	for {
		out, err := m.client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(m.table), FilterExpression: aws.String("begins_with(#pk,:prefix) AND #sk = :meta"), ExpressionAttributeNames: map[string]string{"#pk": m.pkAttribute, "#sk": m.skAttribute}, ExpressionAttributeValues: map[string]dbtypes.AttributeValue{":prefix": avs(m.keyPrefix + "conv#"), ":meta": avs("META")}, ExclusiveStartKey: start})
		if err != nil {
			return nil, fmt.Errorf("dynamodb conversation: list: %w", err)
		}
		for _, item := range out.Items {
			pk := item[m.pkAttribute].(*dbtypes.AttributeValueMemberS).Value
			if rev, _, _ := metadata(item); rev > 0 {
				ids = append(ids, strings.TrimPrefix(pk, m.keyPrefix+"conv#"))
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	return ids, nil
}

const (
	batchWriteLimit      = 25
	maxBatchWriteRetries = 8
	batchWriteRetryBase  = 10 * time.Millisecond
	batchWriteRetryMax   = time.Second
)

func waitBatchWriteRetry(ctx context.Context, attempt int) error {
	delay := batchWriteRetryBase << min(attempt, 6)
	if delay > batchWriteRetryMax {
		delay = batchWriteRetryMax
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *Conversation) Delete(ctx context.Context, id string) error {
	keys, err := m.deleteKeys(ctx, id)
	if err != nil {
		return err
	}
	for len(keys) > 0 {
		n := min(batchWriteLimit, len(keys))
		if err := m.deleteBatch(ctx, keys[:n]); err != nil {
			return err
		}
		keys = keys[n:]
	}
	return nil
}

// deleteKeys reads every page before deletion so consuming a page cannot make
// its LastEvaluatedKey invalid while items are removed from the partition.
func (m *Conversation) deleteKeys(ctx context.Context, id string) ([]map[string]dbtypes.AttributeValue, error) {
	var keys []map[string]dbtypes.AttributeValue
	var start map[string]dbtypes.AttributeValue
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out, err := m.client.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(m.table), KeyConditionExpression: aws.String("#pk = :pk"), ProjectionExpression: aws.String("#pk, #sk"), ExpressionAttributeNames: map[string]string{"#pk": m.pkAttribute, "#sk": m.skAttribute}, ExpressionAttributeValues: map[string]dbtypes.AttributeValue{":pk": avs(m.partition(id))}, ExclusiveStartKey: start})
		if err != nil {
			return nil, fmt.Errorf("dynamodb conversation: delete query: %w", err)
		}
		for _, item := range out.Items {
			pk, ok := item[m.pkAttribute].(*dbtypes.AttributeValueMemberS)
			if !ok || pk.Value != m.partition(id) {
				return nil, errors.New("dynamodb conversation: delete item partition key missing")
			}
			sk, ok := item[m.skAttribute].(*dbtypes.AttributeValueMemberS)
			if !ok {
				return nil, errors.New("dynamodb conversation: delete item sort key missing")
			}
			keys = append(keys, m.key(id, sk.Value))
		}
		if len(out.LastEvaluatedKey) == 0 {
			return keys, nil
		}
		start = out.LastEvaluatedKey
	}
}

func (m *Conversation) deleteBatch(ctx context.Context, keys []map[string]dbtypes.AttributeValue) error {
	requests := make([]dbtypes.WriteRequest, len(keys))
	for i, key := range keys {
		requests[i] = dbtypes.WriteRequest{DeleteRequest: &dbtypes.DeleteRequest{Key: key}}
	}
	pending := map[string][]dbtypes.WriteRequest{m.table: requests}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		out, err := m.client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: pending})
		if err != nil {
			return fmt.Errorf("dynamodb conversation: delete batch: %w", err)
		}
		if len(out.UnprocessedItems) == 0 {
			return nil
		}
		if attempt >= maxBatchWriteRetries {
			return fmt.Errorf("dynamodb conversation: delete batch: unprocessed items remain after %d retries", maxBatchWriteRetries)
		}
		wait := m.waitBatchWriteRetry
		if wait == nil {
			wait = waitBatchWriteRetry
		}
		if err := wait(ctx, attempt); err != nil {
			return fmt.Errorf("dynamodb conversation: delete batch retry: %w", err)
		}
		pending = out.UnprocessedItems
	}
}
