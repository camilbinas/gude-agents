// Package dynamodb provides a DynamoDB-backed agent.ExecutionStore.
//
// Each execution is one item. Create is a conditional put that requires the
// item to be absent, and Save is a conditional put that requires the stored
// version to equal the expected one, so DynamoDB itself arbitrates concurrent
// writers. Reads are strongly consistent.
//
// # Table schema
//
// The caller creates the table with a single-attribute key:
//   - Partition key: "execution_id" (String)
//
// Each item stores:
//   - execution_id: string, the length-prefixed key namespace plus the ID
//   - version: number, the compare-and-swap version
//   - payload: string, the JSON-encoded execution
//
// The package does not read or write any table used by earlier releases.
//
// # Usage
//
//	executions := dynamodb.New(client)
//	executions := dynamodb.New(client, dynamodb.WithTableName("my_executions"))
//
// Import it with an alias when you also use another package named dynamodb:
//
//	ddbexec "github.com/camilbinas/gude-agents/agent/execution/dynamodb"
package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/record"
)

const (
	defaultTable   = "executions"
	keyAttribute   = "execution_id"
	versionAttr    = "version"
	payloadAttr    = "payload"
	createCondExpr = "attribute_not_exists(execution_id)"
	saveCondExpr   = "version = :expected"
)

// Client is the subset of *dynamodb.Client the store needs.
type Client interface {
	GetItem(ctx context.Context, in *awsdynamodb.GetItemInput, opts ...func(*awsdynamodb.Options)) (*awsdynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *awsdynamodb.PutItemInput, opts ...func(*awsdynamodb.Options)) (*awsdynamodb.PutItemOutput, error)
}

var _ agent.ExecutionStore = (*Store)(nil)

// Store is an agent.ExecutionStore backed by DynamoDB.
type Store struct {
	client Client
	table  string
	prefix string
}

// New returns a Store over a DynamoDB client. The caller owns the client's
// configuration and lifecycle (region, credentials, custom endpoints). A nil
// client does not panic: every operation returns an error.
func New(client Client, opts ...Option) *Store {
	cfg := config{table: defaultTable}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Store{client: client, table: cfg.table, prefix: cfg.keyPrefix}
}

// Create stores a new execution at version 1. The ID must be absent.
func (s *Store) Create(ctx context.Context, execution agent.Execution) (agent.Execution, error) {
	if err := s.check(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	payload, err := record.Marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	_, err = s.client.PutItem(ctx, &awsdynamodb.PutItemInput{
		TableName:           aws.String(s.table),
		Item:                s.item(execution.ID, 1, payload),
		ConditionExpression: aws.String(createCondExpr),
	})
	if err != nil {
		if isConditionFailure(err) {
			return agent.Execution{}, record.Conflict("create", execution.ID)
		}
		return agent.Execution{}, record.Wrap("create", err)
	}
	return record.Unmarshal(payload, execution.ID, 1)
}

// Load returns the current execution using a strongly consistent read.
func (s *Store) Load(ctx context.Context, id string) (agent.Execution, error) {
	if err := s.check(id); err != nil {
		return agent.Execution{}, err
	}
	out, err := s.client.GetItem(ctx, &awsdynamodb.GetItemInput{
		TableName:      aws.String(s.table),
		Key:            map[string]dbtypes.AttributeValue{keyAttribute: &dbtypes.AttributeValueMemberS{Value: s.partitionKey(id)}},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return agent.Execution{}, record.Wrap("load", err)
	}
	if len(out.Item) == 0 {
		return agent.Execution{}, record.NotFound(id)
	}
	version, payload, err := decodeItem(out.Item)
	if err != nil {
		return agent.Execution{}, err
	}
	return record.Unmarshal(payload, id, version)
}

// Save replaces the execution when expectedVersion is exactly the stored
// version. The condition is evaluated by DynamoDB atomically with the write; a
// stale or missing version is a conflict.
func (s *Store) Save(ctx context.Context, execution agent.Execution, expectedVersion uint64) (agent.Execution, error) {
	if err := s.check(execution.ID); err != nil {
		return agent.Execution{}, err
	}
	if err := record.CheckExpected(execution.ID, expectedVersion); err != nil {
		return agent.Execution{}, err
	}
	payload, err := record.Marshal(execution)
	if err != nil {
		return agent.Execution{}, err
	}
	next := expectedVersion + 1
	_, err = s.client.PutItem(ctx, &awsdynamodb.PutItemInput{
		TableName:           aws.String(s.table),
		Item:                s.item(execution.ID, next, payload),
		ConditionExpression: aws.String(saveCondExpr),
		ExpressionAttributeValues: map[string]dbtypes.AttributeValue{
			":expected": &dbtypes.AttributeValueMemberN{Value: strconv.FormatUint(expectedVersion, 10)},
		},
	})
	if err != nil {
		if isConditionFailure(err) {
			return agent.Execution{}, record.Conflict("save", execution.ID)
		}
		return agent.Execution{}, record.Wrap("save", err)
	}
	return record.Unmarshal(payload, execution.ID, next)
}

func (s *Store) item(id string, version uint64, payload []byte) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{
		keyAttribute: &dbtypes.AttributeValueMemberS{Value: s.partitionKey(id)},
		versionAttr:  &dbtypes.AttributeValueMemberN{Value: strconv.FormatUint(version, 10)},
		payloadAttr:  &dbtypes.AttributeValueMemberS{Value: string(payload)},
	}
}

// partitionKey length-prefixes the namespace so two prefixes can never produce
// the same key for different (prefix, ID) pairs.
func (s *Store) partitionKey(id string) string {
	return strconv.Itoa(len(s.prefix)) + ":" + s.prefix + id
}

func (s *Store) check(id string) error {
	if s == nil || s.client == nil {
		return errors.New("execution/dynamodb: client is required")
	}
	if s.table == "" {
		return errors.New("execution/dynamodb: table name is required")
	}
	return record.ValidateID(id)
}

func decodeItem(item map[string]dbtypes.AttributeValue) (uint64, []byte, error) {
	versionValue, ok := item[versionAttr].(*dbtypes.AttributeValueMemberN)
	if !ok {
		return 0, nil, errors.New("execution/dynamodb: item is missing its numeric version attribute")
	}
	payloadValue, ok := item[payloadAttr].(*dbtypes.AttributeValueMemberS)
	if !ok {
		return 0, nil, errors.New("execution/dynamodb: item is missing its payload attribute")
	}
	n, err := strconv.ParseInt(versionValue.Value, 10, 64)
	if err != nil {
		return 0, nil, fmt.Errorf("execution/dynamodb: malformed version %q: %w", versionValue.Value, err)
	}
	version, err := record.Version(n)
	if err != nil {
		return 0, nil, err
	}
	return version, []byte(payloadValue.Value), nil
}

func isConditionFailure(err error) bool {
	var conflict *dbtypes.ConditionalCheckFailedException
	return errors.As(err, &conflict)
}
