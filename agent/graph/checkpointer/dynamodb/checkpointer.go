// Package dynamodb provides a DynamoDB-backed implementation of the
// GraphCheckpointer interface for persistent graph execution checkpointing.
//
// # Table Schema
//
// The DynamoDB table must be created by the caller with:
//   - Partition key: "thread_id" (String)
//   - Sort key: "version" (Number)
//
// Each item stores:
//   - thread_id: string — optional prefix + threadID
//   - version: number — monotonically increasing checkpoint version
//   - data: string — JSON-encoded Checkpoint
//
// # Usage
//
//	cp, err := dynamodb.New(awsCfg, "graph_checkpoints")
//	cp, err := dynamodb.New(awsCfg, "graph_checkpoints",
//	    dynamodb.WithKeyPrefix("myapp:"),
//	    dynamodb.WithEndpoint("http://localhost:8000"),
//	)
package dynamodb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/camilbinas/gude-agents/agent/graph"
)

// dynamoDBClient is the interface for DynamoDB operations used by Checkpointer.
// The concrete *dynamodb.Client satisfies this interface.
type dynamoDBClient interface {
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// Compile-time interface check.
var _ graph.GraphCheckpointer = (*Checkpointer)(nil)

// Checkpointer implements graph.GraphCheckpointer using Amazon DynamoDB.
type Checkpointer struct {
	client    dynamoDBClient
	table     string
	keyPrefix string
}

// New creates a new DynamoDB Checkpointer. The table must already exist with
// the expected schema (partition key: "thread_id" String, sort key: "version" Number).
//
// Returns an error if table is empty.
func New(cfg aws.Config, table string, opts ...Option) (*Checkpointer, error) {
	if table == "" {
		return nil, fmt.Errorf("dynamodb checkpointer: table name is required")
	}

	c := &config{
		keyPrefix: "",
	}
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

// Save persists a checkpoint for the given thread. It queries the current max
// version for the thread and assigns version = max + 1.
func (c *Checkpointer) Save(ctx context.Context, threadID string, cp graph.Checkpoint) (graph.Checkpoint, error) {
	pk := c.keyPrefix + threadID

	// Determine next version by querying the latest.
	version, err := c.nextVersion(ctx, pk)
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: save: %w", err)
	}

	cp.ThreadID = threadID
	cp.Version = version

	data, err := json.Marshal(cp)
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: save marshal: %w", err)
	}

	item := map[string]dbtypes.AttributeValue{
		"thread_id": &dbtypes.AttributeValueMemberS{Value: pk},
		"version":   &dbtypes.AttributeValueMemberN{Value: strconv.Itoa(version)},
		"data":      &dbtypes.AttributeValueMemberS{Value: string(data)},
	}

	_, err = c.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.table),
		Item:      item,
	})
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: save: %w", err)
	}

	return cp, nil
}

// Load retrieves the latest checkpoint (highest version) for the given thread.
// Returns graph.ErrCheckpointNotFound if no checkpoints exist.
func (c *Checkpointer) Load(ctx context.Context, threadID string) (graph.Checkpoint, error) {
	pk := c.keyPrefix + threadID

	out, err := c.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.table),
		KeyConditionExpression: aws.String("thread_id = :tid"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":tid": &dbtypes.AttributeValueMemberS{Value: pk},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(1),
	})
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: load: %w", err)
	}

	if len(out.Items) == 0 {
		return graph.Checkpoint{}, graph.ErrCheckpointNotFound
	}

	return c.unmarshalItem(out.Items[0])
}

// LoadAt retrieves the checkpoint at a specific version for the given thread.
// Returns graph.ErrCheckpointNotFound if the version does not exist.
func (c *Checkpointer) LoadAt(ctx context.Context, threadID string, version int) (graph.Checkpoint, error) {
	pk := c.keyPrefix + threadID

	out, err := c.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.table),
		KeyConditionExpression: aws.String("thread_id = :tid AND version = :ver"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":tid": &dbtypes.AttributeValueMemberS{Value: pk},
			":ver": &dbtypes.AttributeValueMemberN{Value: strconv.Itoa(version)},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return graph.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: load at: %w", err)
	}

	if len(out.Items) == 0 {
		return graph.Checkpoint{}, graph.ErrCheckpointNotFound
	}

	return c.unmarshalItem(out.Items[0])
}

// History returns ordered checkpoint metadata for a thread (oldest first).
// Only metadata fields (version, node_name, timestamp) are projected.
func (c *Checkpointer) History(ctx context.Context, threadID string) ([]graph.CheckpointMeta, error) {
	pk := c.keyPrefix + threadID

	var metas []graph.CheckpointMeta
	var lastKey map[string]dbtypes.AttributeValue

	for {
		input := &dynamodb.QueryInput{
			TableName:              aws.String(c.table),
			KeyConditionExpression: aws.String("thread_id = :tid"),
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":tid": &dbtypes.AttributeValueMemberS{Value: pk},
			},
			ScanIndexForward:  aws.Bool(true),
			ExclusiveStartKey: lastKey,
		}

		out, err := c.client.Query(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("dynamodb checkpointer: history: %w", err)
		}

		for _, item := range out.Items {
			cp, err := c.unmarshalItem(item)
			if err != nil {
				return nil, err
			}
			metas = append(metas, graph.CheckpointMeta{
				Version:   cp.Version,
				NodeName:  cp.NodeName,
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

// List returns all distinct thread IDs that have stored checkpoints.
// This performs a full-table Scan — be aware of cost implications for large tables.
func (c *Checkpointer) List(ctx context.Context) ([]string, error) {
	seen := make(map[string]bool)
	var lastKey map[string]dbtypes.AttributeValue

	for {
		input := &dynamodb.ScanInput{
			TableName:            aws.String(c.table),
			ProjectionExpression: aws.String("thread_id"),
			ExclusiveStartKey:    lastKey,
		}

		out, err := c.client.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("dynamodb checkpointer: list: %w", err)
		}

		for _, item := range out.Items {
			attr, ok := item["thread_id"]
			if !ok {
				continue
			}
			sv, ok := attr.(*dbtypes.AttributeValueMemberS)
			if !ok {
				continue
			}
			id := strings.TrimPrefix(sv.Value, c.keyPrefix)
			seen[id] = true
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

// Delete removes all checkpoints for a thread by querying all versions
// and deleting each item individually.
func (c *Checkpointer) Delete(ctx context.Context, threadID string) error {
	pk := c.keyPrefix + threadID

	var lastKey map[string]dbtypes.AttributeValue

	for {
		out, err := c.client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(c.table),
			KeyConditionExpression: aws.String("thread_id = :tid"),
			ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
				":tid": &dbtypes.AttributeValueMemberS{Value: pk},
			},
			ProjectionExpression: aws.String("thread_id, version"),
			ExclusiveStartKey:    lastKey,
		})
		if err != nil {
			return fmt.Errorf("dynamodb checkpointer: delete query: %w", err)
		}

		for _, item := range out.Items {
			_, err := c.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
				TableName: aws.String(c.table),
				Key: map[string]dbtypes.AttributeValue{
					"thread_id": item["thread_id"],
					"version":   item["version"],
				},
			})
			if err != nil {
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

// nextVersion queries the latest version for a thread and returns max + 1.
func (c *Checkpointer) nextVersion(ctx context.Context, pk string) (int, error) {
	out, err := c.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.table),
		KeyConditionExpression: aws.String("thread_id = :tid"),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":tid": &dbtypes.AttributeValueMemberS{Value: pk},
		},
		ScanIndexForward:     aws.Bool(false),
		Limit:                aws.Int32(1),
		ProjectionExpression: aws.String("version"),
	})
	if err != nil {
		return 0, err
	}

	if len(out.Items) == 0 {
		return 1, nil
	}

	verAttr, ok := out.Items[0]["version"]
	if !ok {
		return 1, nil
	}
	nv, ok := verAttr.(*dbtypes.AttributeValueMemberN)
	if !ok {
		return 1, nil
	}
	v, err := strconv.Atoi(nv.Value)
	if err != nil {
		return 1, nil
	}
	return v + 1, nil
}

// unmarshalItem extracts a Checkpoint from a DynamoDB item.
func (c *Checkpointer) unmarshalItem(item map[string]dbtypes.AttributeValue) (graph.Checkpoint, error) {
	dataAttr, ok := item["data"]
	if !ok {
		return graph.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: item missing 'data' attribute")
	}
	sv, ok := dataAttr.(*dbtypes.AttributeValueMemberS)
	if !ok {
		return graph.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: 'data' attribute is not a string")
	}

	var cp graph.Checkpoint
	if err := json.Unmarshal([]byte(sv.Value), &cp); err != nil {
		return graph.Checkpoint{}, fmt.Errorf("dynamodb checkpointer: unmarshal: %w", err)
	}
	return cp, nil
}
