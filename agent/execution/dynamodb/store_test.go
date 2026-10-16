package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/conformance"
)

// fakeDynamo is an in-memory stand-in for the two DynamoDB operations the store
// uses. It evaluates exactly the two condition expressions the store emits and
// fails loudly on anything else, so it cannot silently drift from the store.
type fakeDynamo struct {
	mu      sync.Mutex
	items   map[string]map[string]dbtypes.AttributeValue
	puts    int
	failPut error
}

func newFakeDynamo() *fakeDynamo {
	return &fakeDynamo{items: make(map[string]map[string]dbtypes.AttributeValue)}
}

func keyOf(item map[string]dbtypes.AttributeValue) (string, error) {
	key, ok := item[keyAttribute].(*dbtypes.AttributeValueMemberS)
	if !ok {
		return "", errors.New("fake: item has no string key")
	}
	return key.Value, nil
}

func copyItem(in map[string]dbtypes.AttributeValue) map[string]dbtypes.AttributeValue {
	out := make(map[string]dbtypes.AttributeValue, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func conditionFailed() error {
	message := "The conditional request failed"
	return &dbtypes.ConditionalCheckFailedException{Message: &message}
}

func (f *fakeDynamo) PutItem(_ context.Context, in *awsdynamodb.PutItemInput, _ ...func(*awsdynamodb.Options)) (*awsdynamodb.PutItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	if f.failPut != nil {
		return nil, f.failPut
	}
	key, err := keyOf(in.Item)
	if err != nil {
		return nil, err
	}
	existing, exists := f.items[key]
	switch expr := derefString(in.ConditionExpression); expr {
	case createCondExpr:
		if exists {
			return nil, conditionFailed()
		}
	case saveCondExpr:
		expected, ok := in.ExpressionAttributeValues[":expected"].(*dbtypes.AttributeValueMemberN)
		if !ok {
			return nil, errors.New("fake: save has no :expected value")
		}
		current, ok := existing[versionAttr].(*dbtypes.AttributeValueMemberN)
		if !exists || !ok || current.Value != expected.Value {
			return nil, conditionFailed()
		}
	default:
		return nil, fmt.Errorf("fake: unsupported condition expression %q", expr)
	}
	f.items[key] = copyItem(in.Item)
	return &awsdynamodb.PutItemOutput{}, nil
}

func (f *fakeDynamo) GetItem(_ context.Context, in *awsdynamodb.GetItemInput, _ ...func(*awsdynamodb.Options)) (*awsdynamodb.GetItemOutput, error) {
	if in.ConsistentRead == nil || !*in.ConsistentRead {
		return nil, errors.New("fake: GetItem must be a strongly consistent read")
	}
	key, err := keyOf(in.Key)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &awsdynamodb.GetItemOutput{Item: copyItem(f.items[key])}, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func newStoreForConformance(*testing.T) agent.ExecutionStore { return New(newFakeDynamo()) }

func TestConformance(t *testing.T) { conformance.Run(t, newStoreForConformance) }

func TestAgentProtocol(t *testing.T) { conformance.RunAgent(t, newStoreForConformance) }

func TestStoreLaysOutTheDocumentedItem(t *testing.T) {
	fake := newFakeDynamo()
	store := New(fake, WithKeyPrefix("app:"))
	created, err := store.Create(context.Background(), conformance.Full("exec-1"))
	if err != nil {
		t.Fatal(err)
	}
	item := fake.items["4:app:exec-1"]
	if item == nil {
		t.Fatalf("no item under the length-prefixed key; items = %v", fake.items)
	}
	if version := item[versionAttr].(*dbtypes.AttributeValueMemberN).Value; version != "1" {
		t.Fatalf("stored version = %q, want 1", version)
	}
	var stored map[string]json.RawMessage
	payload := item[payloadAttr].(*dbtypes.AttributeValueMemberS).Value
	if err := json.Unmarshal([]byte(payload), &stored); err != nil {
		t.Fatal(err)
	}
	if string(stored["version"]) != "0" {
		t.Fatalf("payload version = %s; the version attribute is authoritative", stored["version"])
	}
	if created.Version != 1 {
		t.Fatalf("created version = %d", created.Version)
	}
}

func TestKeyPrefixesIsolateExecutions(t *testing.T) {
	fake := newFakeDynamo()
	one, two := New(fake, WithKeyPrefix("a")), New(fake, WithKeyPrefix("a:b"))
	ctx := context.Background()
	if _, err := one.Create(ctx, conformance.Full("b:x")); err != nil {
		t.Fatal(err)
	}
	// With naive concatenation "a"+"b:x" and "a:b"+"x" would collide.
	if _, err := two.Create(ctx, conformance.Full("x")); err != nil {
		t.Fatalf("overlapping prefixes collided: %v", err)
	}
	if _, err := two.Load(ctx, "b:x"); !errors.Is(err, agent.ErrExecutionNotFound) {
		t.Fatalf("a different prefix observed the execution: %v", err)
	}
}

func TestBackendFailuresAreNotClassifiedAsConflicts(t *testing.T) {
	fake := newFakeDynamo()
	store := New(fake)
	ctx := context.Background()
	created, err := store.Create(ctx, conformance.Full("exec-err"))
	if err != nil {
		t.Fatal(err)
	}
	fake.failPut = errors.New("throttled")
	if _, err := store.Save(ctx, created, created.Version); err == nil || errors.Is(err, agent.ErrExecutionConflict) {
		t.Fatalf("Save error = %v; an infrastructure failure must not look like a CAS conflict", err)
	}
	if _, err := store.Create(ctx, conformance.Full("exec-err-2")); err == nil || errors.Is(err, agent.ErrExecutionConflict) {
		t.Fatalf("Create error = %v; an infrastructure failure must not look like a duplicate", err)
	}
}

func TestMisconfiguredStoreReturnsErrors(t *testing.T) {
	var client Client
	for name, store := range map[string]*Store{
		"nil client":  New(client),
		"nil store":   nil,
		"empty table": {client: newFakeDynamo()},
	} {
		if _, err := store.Load(context.Background(), "x"); err == nil {
			t.Errorf("%s: Load returned nil error", name)
		}
	}
	if store := New(newFakeDynamo(), WithTableName("")); store.table != defaultTable {
		t.Errorf("empty table name should keep the default, got %q", store.table)
	}
}
