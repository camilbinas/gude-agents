// Package dynamodb provides a DynamoDB-backed checkpoint.Checkpointer.
//
// # Table Schema
//
// The DynamoDB table must be created by the caller with:
//   - Partition key: "thread_id" (String)
//   - Sort key: "version" (Number)
//
// Each item stores:
//   - thread_id: string — length-prefixed namespace + threadID
//   - version: number — monotonically increasing checkpoint version
//   - data: string — JSON-encoded Checkpoint
//
// The entire Checkpoint is stored as one JSON value, so consumer-owned fields in
// the opaque Extra payload are preserved without backend interpretation.
//
// # Usage
//
//	cp, err := dynamodb.New(awsCfg, "checkpoints")
//	cp, err := dynamodb.New(awsCfg, "checkpoints",
//	    dynamodb.WithKeyPrefix("myapp:"),
//	    dynamodb.WithEndpoint("http://localhost:8000"),
//	)
package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
)

type dynamoDBClient interface {
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

var _ checkpoint.Checkpointer = (*Checkpointer)(nil)

type Checkpointer struct {
	client    dynamoDBClient
	table     string
	keyPrefix string
}

// New creates a DynamoDB Checkpointer. The table must already exist with the
// expected schema (partition key "thread_id" String, sort key "version" Number).
func New(cfg aws.Config, table string, opts ...Option) (*Checkpointer, error) {
	if table == "" {
		return nil, fmt.Errorf("dynamodb checkpointer: table name is required")
	}

	c := &config{keyPrefix: ""}
	for _, o := range opts {
		o(c)
	}

	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if c.endpoint != "" {
			o.BaseEndpoint = aws.String(c.endpoint)
		}
	})

	return &Checkpointer{
		client:    client,
		table:     table,
		keyPrefix: c.keyPrefix,
	}, nil
}

// Save appends a checkpoint using a conditional write so concurrent callers
// cannot overwrite the same version.
func (c *Checkpointer) Save(ctx context.Context, threadID string, cp checkpoint.Checkpoint) (checkpoint.Checkpoint, error) {
	if threadID == "" {
		return checkpoint.Checkpoint{}, checkpoint.ErrThreadIDRequired
	}

	pk := c.partitionKey(threadID)
	cp.ThreadID = threadID
	if cp.Timestamp.IsZero() {
		cp.Timestamp = time.Now()
	}

	for {
		version, err := c.nextVersion(ctx, pk)
		if err != nil {
			return checkpoint.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: save: %w", err)
		}
		cp.Version = version

		data, err := json.Marshal(cp)
		if err != nil {
			return checkpoint.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: save marshal: %w", err)
		}

		item := map[string]dbtypes.AttributeValue{
			"thread_id": &dbtypes.AttributeValueMemberS{Value: pk},
			"version":   &dbtypes.AttributeValueMemberN{Value: strconv.Itoa(version)},
			"data":      &dbtypes.AttributeValueMemberS{Value: string(data)},
		}

		_, err = c.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:           aws.String(c.table),
			Item:                item,
			ConditionExpression: aws.String("attribute_not_exists(thread_id) AND attribute_not_exists(version)"),
		})
		if err == nil {
			return cp, nil
		}

		var conflict *dbtypes.ConditionalCheckFailedException
		if errors.As(err, &conflict) {
			if ctx.Err() != nil {
				return checkpoint.Checkpoint{}, ctx.Err()
			}
			continue
		}
		return checkpoint.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: save: %w", err)
	}
}

// Load returns the highest-versioned checkpoint for the thread.
func (c *Checkpointer) Load(ctx context.Context, threadID string) (checkpoint.Checkpoint, error) {
	out, err := c.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.table),
		KeyConditionExpression: aws.String("thread_id = :tid"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":tid": &dbtypes.AttributeValueMemberS{Value: c.partitionKey(threadID)},
		},
		ScanIndexForward: aws.Bool(false),
		ConsistentRead:   aws.Bool(true),
		Limit:            aws.Int32(1),
	})
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: load: %w", err)
	}
	if len(out.Items) == 0 {
		return checkpoint.Checkpoint{}, checkpoint.ErrNotFound
	}
	return c.unmarshalItem(out.Items[0])
}

// LoadAt returns the checkpoint at an exact version.
func (c *Checkpointer) LoadAt(ctx context.Context, threadID string, version int) (checkpoint.Checkpoint, error) {
	out, err := c.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.table),
		KeyConditionExpression: aws.String("thread_id = :tid AND version = :ver"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":tid": &dbtypes.AttributeValueMemberS{Value: c.partitionKey(threadID)},
			":ver": &dbtypes.AttributeValueMemberN{Value: strconv.Itoa(version)},
		},
		ConsistentRead: aws.Bool(true),
		Limit:          aws.Int32(1),
	})
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: load at: %w", err)
	}
	if len(out.Items) == 0 {
		return checkpoint.Checkpoint{}, checkpoint.ErrNotFound
	}
	return c.unmarshalItem(out.Items[0])
}

// History returns metadata for every checkpoint on the thread, oldest first.
func (c *Checkpointer) History(ctx context.Context, threadID string) ([]checkpoint.Meta, error) {
	pk := c.partitionKey(threadID)

	var metas []checkpoint.Meta
	var lastKey map[string]dbtypes.AttributeValue

	for {
		out, err := c.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(c.table),
			KeyConditionExpression: aws.String("thread_id = :tid"),
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":tid": &dbtypes.AttributeValueMemberS{Value: pk},
			},
			ScanIndexForward:  aws.Bool(true),
			ConsistentRead:    aws.Bool(true),
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("dynamodb checkpointer: history: %w", err)
		}

		for _, item := range out.Items {
			cp, err := c.unmarshalItem(item)
			if err != nil {
				return nil, err
			}
			metas = append(metas, checkpoint.Meta{
				Version:   cp.Version,
				Label:     cp.Label,
				Timestamp: cp.Timestamp,
			})
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	return metas, nil
}

// List returns the distinct thread IDs that have stored checkpoints.
//
// This performs a full-table Scan. See checkpoint.Checkpointer for guidance.
func (c *Checkpointer) List(ctx context.Context) ([]string, error) {
	prefix := c.partitionPrefix()
	seen := make(map[string]bool)
	var lastKey map[string]dbtypes.AttributeValue

	for {
		out, err := c.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:            aws.String(c.table),
			ProjectionExpression: aws.String("thread_id"),
			ConsistentRead:       aws.Bool(true),
			ExclusiveStartKey:    lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("dynamodb checkpointer: list: %w", err)
		}

		for _, item := range out.Items {
			sv, ok := item["thread_id"].(*dbtypes.AttributeValueMemberS)
			if !ok || !strings.HasPrefix(sv.Value, prefix) {
				continue
			}
			seen[strings.TrimPrefix(sv.Value, prefix)] = true
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, nil
}

// Delete removes every checkpoint for the thread, deleting each version item.
func (c *Checkpointer) Delete(ctx context.Context, threadID string) error {
	pk := c.partitionKey(threadID)
	var lastKey map[string]dbtypes.AttributeValue

	for {
		out, err := c.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(c.table),
			KeyConditionExpression: aws.String("thread_id = :tid"),
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":tid": &dbtypes.AttributeValueMemberS{Value: pk},
			},
			ProjectionExpression: aws.String("thread_id, version"),
			ConsistentRead:       aws.Bool(true),
			ExclusiveStartKey:    lastKey,
		})
		if err != nil {
			return fmt.Errorf("dynamodb checkpointer: delete query: %w", err)
		}

		for _, item := range out.Items {
			if _, err := c.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
				TableName: aws.String(c.table),
				Key: map[string]dbtypes.AttributeValue{
					"thread_id": item["thread_id"],
					"version":   item["version"],
				},
			}); err != nil {
				return fmt.Errorf("dynamodb checkpointer: delete item: %w", err)
			}
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	return nil
}

func (c *Checkpointer) partitionPrefix() string {
	return strconv.Itoa(len(c.keyPrefix)) + ":" + c.keyPrefix
}

func (c *Checkpointer) partitionKey(threadID string) string {
	return c.partitionPrefix() + threadID
}

// nextVersion returns max(version)+1 for a thread, or 1 when it has none.
func (c *Checkpointer) nextVersion(ctx context.Context, pk string) (int, error) {
	out, err := c.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.table),
		KeyConditionExpression: aws.String("thread_id = :tid"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":tid": &dbtypes.AttributeValueMemberS{Value: pk},
		},
		ScanIndexForward:     aws.Bool(false),
		ConsistentRead:       aws.Bool(true),
		Limit:                aws.Int32(1),
		ProjectionExpression: aws.String("version"),
	})
	if err != nil {
		return 0, err
	}
	if len(out.Items) == 0 {
		return 1, nil
	}

	nv, ok := out.Items[0]["version"].(*dbtypes.AttributeValueMemberN)
	if !ok {
		return 1, nil
	}
	v, err := strconv.Atoi(nv.Value)
	if err != nil {
		return 1, nil
	}
	return v + 1, nil
}

func (c *Checkpointer) unmarshalItem(item map[string]dbtypes.AttributeValue) (checkpoint.Checkpoint, error) {
	sv, ok := item["data"].(*dbtypes.AttributeValueMemberS)
	if !ok {
		return checkpoint.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: item 'data' attribute missing or not a string")
	}

	var cp checkpoint.Checkpoint
	if err := json.Unmarshal([]byte(sv.Value), &cp); err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: unmarshal: %w", err)
	}
	return cp, nil
}
