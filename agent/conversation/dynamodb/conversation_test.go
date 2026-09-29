package dynamodb

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/camilbinas/gude-agents/agent"
)

type mockDynamoDBClient struct {
	mu                 sync.Mutex
	items              map[string]map[string]dbtypes.AttributeValue
	pkAttr             string
	putErr             error
	getErr             error
	deleteErr          error
	scanErr            error
	lastConsistentRead bool
}

func newMockDynamoDBClient() *mockDynamoDBClient {
	return &mockDynamoDBClient{items: map[string]map[string]dbtypes.AttributeValue{}, pkAttr: "conversation_id"}
}

func (m *mockDynamoDBClient) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return nil, m.putErr
	}
	pk := in.Item[m.pkAttr].(*dbtypes.AttributeValueMemberS).Value
	existing, exists := m.items[pk]
	if strings.Contains(*in.ConditionExpression, "attribute_not_exists(#pk)") {
		_, hasRevision := existing["revision"]
		if exists && hasRevision {
			return nil, &validationError{code: "ConditionalCheckFailedException", message: "exists"}
		}
	} else {
		expected := in.ExpressionAttributeValues[":expected"].(*dbtypes.AttributeValueMemberN).Value
		current, hasRevision := existing["revision"].(*dbtypes.AttributeValueMemberN)
		if !exists || !hasRevision || current.Value != expected {
			return nil, &validationError{code: "ConditionalCheckFailedException", message: "revision mismatch"}
		}
	}
	copied := make(map[string]dbtypes.AttributeValue, len(in.Item))
	for k, v := range in.Item {
		copied[k] = v
	}
	m.items[pk] = copied
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockDynamoDBClient) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	m.lastConsistentRead = in.ConsistentRead != nil && *in.ConsistentRead
	return &dynamodb.GetItemOutput{Item: m.items[in.Key[m.pkAttr].(*dbtypes.AttributeValueMemberS).Value]}, nil
}

func (m *mockDynamoDBClient) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	delete(m.items, in.Key[m.pkAttr].(*dbtypes.AttributeValueMemberS).Value)
	return &dynamodb.DeleteItemOutput{}, nil
}

func (m *mockDynamoDBClient) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scanErr != nil {
		return nil, m.scanErr
	}
	prefix := in.ExpressionAttributeValues[":prefix"].(*dbtypes.AttributeValueMemberS).Value
	var items []map[string]dbtypes.AttributeValue
	for key, item := range m.items {
		if strings.HasPrefix(key, prefix) {
			items = append(items, item)
		}
	}
	return &dynamodb.ScanOutput{Items: items}, nil
}

type validationError struct{ code, message string }

func (e *validationError) Error() string                 { return e.message }
func (e *validationError) ErrorCode() string             { return e.code }
func (e *validationError) ErrorMessage() string          { return e.message }
func (e *validationError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func testStore(mock *mockDynamoDBClient) *Conversation {
	return &Conversation{client: mock, table: "test", keyPrefix: "gude:", ttlAttribute: "ttl", pkAttribute: mock.pkAttr}
}

func TestNew(t *testing.T) {
	if _, err := New(aws.Config{}, ""); err == nil {
		t.Fatal("expected empty table error")
	}
	m, err := New(aws.Config{}, "table")
	if err != nil || m.keyPrefix != "gude:" || m.pkAttribute != "conversation_id" {
		t.Fatalf("New = %+v, %v", m, err)
	}
}

func TestSaveLoadRevisionAndConflict(t *testing.T) {
	mock := newMockDynamoDBClient()
	m := testStore(mock)
	ctx := context.Background()
	messages := []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "one"}}}}

	missing, err := m.Load(ctx, "missing")
	if err != nil || missing.Messages == nil || missing.Revision != 0 {
		t.Fatalf("missing = %+v, %v", missing, err)
	}
	rev, err := m.Save(ctx, "conv", messages, 0)
	if err != nil || rev != 1 {
		t.Fatalf("first save = %d, %v", rev, err)
	}
	rev, err = m.Save(ctx, "conv", messages, rev)
	if err != nil || rev != 2 {
		t.Fatalf("second save = %d, %v", rev, err)
	}
	if _, err := m.Save(ctx, "conv", messages, 1); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("stale save = %v", err)
	}
	snapshot, err := m.Load(ctx, "conv")
	if err != nil || snapshot.Revision != 2 || len(snapshot.Messages) != 1 {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
}

func TestSaveAttributesTTLAndCustomKey(t *testing.T) {
	mock := newMockDynamoDBClient()
	mock.pkAttr = "id"
	m := &Conversation{client: mock, table: "test", keyPrefix: "p:", ttl: time.Hour, ttlAttribute: "expires", pkAttribute: "id"}
	rev, err := m.Save(context.Background(), "conv", []agent.Message{}, 0)
	if err != nil || rev != 1 {
		t.Fatalf("Save = %d, %v", rev, err)
	}
	item := mock.items["p:conv"]
	if item["id"] == nil || item["expires"] == nil || item["messages"].(*dbtypes.AttributeValueMemberS).Value != "[]" {
		t.Fatalf("item = %+v", item)
	}
	if item["revision"].(*dbtypes.AttributeValueMemberN).Value != strconv.Itoa(1) {
		t.Fatalf("revision attribute = %+v", item["revision"])
	}
}

func TestListAndDelete(t *testing.T) {
	m := testStore(newMockDynamoDBClient())
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := m.Save(ctx, id, []agent.Message{}, 0); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := m.List(ctx)
	if err != nil || len(ids) != 2 {
		t.Fatalf("List = %v, %v", ids, err)
	}
	if err := m.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	mock := newMockDynamoDBClient()
	m := testStore(mock)
	mock.putErr = errors.New("down")
	if _, err := m.Save(context.Background(), "x", nil, 0); err == nil || !strings.Contains(err.Error(), "save") {
		t.Fatalf("save error = %v", err)
	}
	mock.putErr = &validationError{code: "ValidationException", message: "Item size has exceeded the maximum allowed size"}
	if _, err := m.Save(context.Background(), "x", nil, 0); err == nil || !strings.Contains(err.Error(), "item too large") {
		t.Fatalf("size error = %v", err)
	}
	mock.putErr = nil
	mock.getErr = errors.New("down")
	if _, err := m.Load(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "load") {
		t.Fatalf("load error = %v", err)
	}
	mock.getErr = nil
	mock.deleteErr = errors.New("down")
	if err := m.Delete(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "delete") {
		t.Fatalf("delete error = %v", err)
	}
}

func TestConversationManagerCompatibility(t *testing.T) {
	var _ agent.ConversationManager = (*Conversation)(nil)
}

func TestConcurrentCASOneWinner(t *testing.T) {
	store := testStore(newMockDynamoDBClient())
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := store.Save(ctx, "race", []agent.Message{}, 0)
			errs <- err
		}()
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-errs
		if err == nil {
			successes++
		} else if errors.Is(err, agent.ErrConversationConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1 and 1", successes, conflicts)
	}
}

func TestLegacyItemLoadsConsistentlyAndUpgradesWithCAS(t *testing.T) {
	mock := newMockDynamoDBClient()
	mock.items["gude:legacy"] = map[string]dbtypes.AttributeValue{
		"conversation_id": &dbtypes.AttributeValueMemberS{Value: "gude:legacy"},
		"messages":        &dbtypes.AttributeValueMemberS{Value: `[{"role":"user","content":[{"type":"text","text":"legacy"}]}]`},
	}
	m := testStore(mock)
	snapshot, err := m.Load(context.Background(), "legacy")
	if err != nil || snapshot.Revision != 0 || len(snapshot.Messages) != 1 {
		t.Fatalf("legacy snapshot = %+v, %v", snapshot, err)
	}
	if !mock.lastConsistentRead {
		t.Fatal("Load did not request a strongly consistent read")
	}
	revision, err := m.Save(context.Background(), "legacy", snapshot.Messages, 0)
	if err != nil || revision != 1 {
		t.Fatalf("upgrade Save = %d, %v", revision, err)
	}
	if _, err := m.Save(context.Background(), "legacy", snapshot.Messages, 0); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("second revision-zero Save = %v", err)
	}
}
