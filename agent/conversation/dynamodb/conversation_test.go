package dynamodb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	smithy "github.com/aws/smithy-go"
	"github.com/camilbinas/gude-agents/agent"
)

// mockDynamoDBClient is an in-memory implementation of the dynamoDBClient interface for testing.
type mockDynamoDBClient struct {
	items  map[string]map[string]dbtypes.AttributeValue
	pkAttr string // partition key attribute name, defaults to "conversation_id"

	putErr    error
	getErr    error
	deleteErr error
	scanErr   error
}

func newMockDynamoDBClient() *mockDynamoDBClient {
	return &mockDynamoDBClient{
		items:  make(map[string]map[string]dbtypes.AttributeValue),
		pkAttr: "conversation_id",
	}
}

func (m *mockDynamoDBClient) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if m.putErr != nil {
		return nil, m.putErr
	}
	pk := in.Item[m.pkAttr].(*dbtypes.AttributeValueMemberS).Value
	// Deep-copy the item map.
	copied := make(map[string]dbtypes.AttributeValue, len(in.Item))
	for k, v := range in.Item {
		copied[k] = v
	}
	m.items[pk] = copied
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockDynamoDBClient) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	pk := in.Key[m.pkAttr].(*dbtypes.AttributeValueMemberS).Value
	item, ok := m.items[pk]
	if !ok {
		return &dynamodb.GetItemOutput{}, nil
	}
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func (m *mockDynamoDBClient) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	pk := in.Key[m.pkAttr].(*dbtypes.AttributeValueMemberS).Value
	delete(m.items, pk)
	return &dynamodb.DeleteItemOutput{}, nil
}

func (m *mockDynamoDBClient) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if m.scanErr != nil {
		return nil, m.scanErr
	}
	prefix := in.ExpressionAttributeValues[":prefix"].(*dbtypes.AttributeValueMemberS).Value
	var items []map[string]dbtypes.AttributeValue
	for pk, item := range m.items {
		if strings.HasPrefix(pk, prefix) {
			items = append(items, item)
		}
	}
	return &dynamodb.ScanOutput{Items: items}, nil
}

// --- Constructor tests ---

// TestNewDynamoDBMemory_EmptyTable verifies that an empty table name returns an error.
// Req 7.4
func TestNewDynamoDBMemory_EmptyTable(t *testing.T) {
	_, err := New(aws.Config{}, "")
	if err == nil {
		t.Fatal("expected error for empty table, got nil")
	}
	if !strings.Contains(err.Error(), "table name is required") {
		t.Errorf("expected error to contain %q, got %q", "table name is required", err.Error())
	}
}

// TestNewDynamoDBMemory_LazyConstruction verifies that valid args return a non-nil DynamoDBMemory
// without making any network calls.
// Req 7.3
func TestNewDynamoDBMemory_LazyConstruction(t *testing.T) {
	m, err := New(aws.Config{}, "my-table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil DynamoDBMemory")
	}
}

// TestDynamoDBMemory_DefaultKeyPrefix verifies that keyPrefix defaults to "gude:".
// Req 8.1
func TestDynamoDBMemory_DefaultKeyPrefix(t *testing.T) {
	m, err := New(aws.Config{}, "my-table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.keyPrefix != "gude:" {
		t.Errorf("expected keyPrefix %q, got %q", "gude:", m.keyPrefix)
	}
}

// TestDynamoDBMemory_DefaultPKAttribute verifies that pkAttribute defaults to "conversation_id".
func TestDynamoDBMemory_DefaultPKAttribute(t *testing.T) {
	m, err := New(aws.Config{}, "my-table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.pkAttribute != "conversation_id" {
		t.Errorf("expected pkAttribute %q, got %q", "conversation_id", m.pkAttribute)
	}
}

// TestDynamoDBMemory_WithPartitionKey verifies that a custom partition key attribute name is used in PutItem.
func TestDynamoDBMemory_WithPartitionKey(t *testing.T) {
	mock := newMockDynamoDBClient()
	mock.pkAttr = "id"
	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "id",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	item, ok := mock.items["gude:conv1"]
	if !ok {
		t.Fatal("expected item to be stored")
	}
	if _, hasCustomPK := item["id"]; !hasCustomPK {
		t.Error("expected custom partition key attribute \"id\" to be present in item")
	}
	if _, hasDefaultPK := item["conversation_id"]; hasDefaultPK {
		t.Error("expected default partition key attribute \"conversation_id\" to be absent when custom attribute is set")
	}
}

// TestDynamoDBMemory_WithTTL verifies that when TTL is set, PutItem includes the TTL attribute.
// Req 8.4
func TestDynamoDBMemory_WithTTL(t *testing.T) {
	mock := newMockDynamoDBClient()
	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttl:          24 * time.Hour,
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	item, ok := mock.items["gude:conv1"]
	if !ok {
		t.Fatal("expected item to be stored")
	}
	if _, hasTTL := item["ttl"]; !hasTTL {
		t.Error("expected TTL attribute to be present in item")
	}
}

// TestDynamoDBMemory_NoTTL verifies that when no TTL is set, PutItem omits the TTL attribute.
// Req 8.5
func TestDynamoDBMemory_NoTTL(t *testing.T) {
	mock := newMockDynamoDBClient()
	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttl:          0, // no TTL
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	item, ok := mock.items["gude:conv1"]
	if !ok {
		t.Fatal("expected item to be stored")
	}
	if _, hasTTL := item["ttl"]; hasTTL {
		t.Error("expected TTL attribute to be absent when no TTL is configured")
	}
}

// TestDynamoDBMemory_WithTTLAttribute verifies that a custom TTL attribute name is used in PutItem.
// Req 8.6
func TestDynamoDBMemory_WithTTLAttribute(t *testing.T) {
	mock := newMockDynamoDBClient()
	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttl:          1 * time.Hour,
		ttlAttribute: "expires_at",
		pkAttribute:  "conversation_id",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	item, ok := mock.items["gude:conv1"]
	if !ok {
		t.Fatal("expected item to be stored")
	}
	if _, hasCustomAttr := item["expires_at"]; !hasCustomAttr {
		t.Error("expected custom TTL attribute \"expires_at\" to be present in item")
	}
	if _, hasDefaultAttr := item["ttl"]; hasDefaultAttr {
		t.Error("expected default TTL attribute \"ttl\" to be absent when custom attribute is set")
	}
}

// --- Error wrapping tests ---

// TestDynamoDBMemory_Save_ErrorWrapping verifies that a generic PutItem error is wrapped with "dynamodb conversation: save".
// Req 9.3
func TestDynamoDBMemory_Save_ErrorWrapping(t *testing.T) {
	mock := newMockDynamoDBClient()
	mock.putErr = errors.New("dynamodb unavailable")

	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "dynamodb conversation: save") {
		t.Errorf("expected error to contain %q, got %q", "dynamodb conversation: save", err.Error())
	}
}

// TestDynamoDBMemory_Load_ErrorWrapping verifies that a GetItem error is wrapped with "dynamodb conversation: load".
// Req 10.3
func TestDynamoDBMemory_Load_ErrorWrapping(t *testing.T) {
	mock := newMockDynamoDBClient()
	mock.getErr = errors.New("dynamodb internal error")

	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	_, err := m.Load(context.Background(), "conv1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "dynamodb conversation: load") {
		t.Errorf("expected error to contain %q, got %q", "dynamodb conversation: load", err.Error())
	}
}

// TestDynamoDBMemory_Delete_ErrorWrapping verifies that a DeleteItem error is wrapped with "dynamodb conversation: delete".
// Req 11.5
func TestDynamoDBMemory_Delete_ErrorWrapping(t *testing.T) {
	mock := newMockDynamoDBClient()
	mock.deleteErr = errors.New("dynamodb delete error")

	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	err := m.Delete(context.Background(), "conv1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "dynamodb conversation: delete") {
		t.Errorf("expected error to contain %q, got %q", "dynamodb conversation: delete", err.Error())
	}
}

// --- Not-found and edge case tests ---

// TestDynamoDBMemory_Load_NotFound verifies that a missing item returns an empty slice and nil error.
// Req 10.2
func TestDynamoDBMemory_Load_NotFound(t *testing.T) {
	mock := newMockDynamoDBClient()

	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	msgs, err := m.Load(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if msgs == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty slice, got %d messages", len(msgs))
	}
}

// TestDynamoDBMemory_Save_EmptySlice verifies that saving an empty slice writes "[]" in the messages attribute.
// Req 9.2
func TestDynamoDBMemory_Save_EmptySlice(t *testing.T) {
	mock := newMockDynamoDBClient()

	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	item, ok := mock.items["gude:conv1"]
	if !ok {
		t.Fatal("expected item to be stored")
	}
	msgAttr, ok := item["messages"]
	if !ok {
		t.Fatal("expected messages attribute to be present")
	}
	sv, ok := msgAttr.(*dbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatal("expected messages attribute to be a String type")
	}
	if sv.Value != "[]" {
		t.Errorf("expected messages attribute value %q, got %q", "[]", sv.Value)
	}
}

// TestDynamoDBMemory_Delete_NonExistent verifies that deleting a key that was never saved returns nil error.
// Req 11.4
func TestDynamoDBMemory_Delete_NonExistent(t *testing.T) {
	mock := newMockDynamoDBClient()

	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	err := m.Delete(context.Background(), "never-saved")
	if err != nil {
		t.Errorf("expected nil error for non-existent key, got %v", err)
	}
}

// --- Item size limit tests ---

// validationError is a smithy.GenericAPIError that simulates a DynamoDB ValidationException.
type validationError struct {
	code    string
	message string
}

func (e *validationError) Error() string        { return e.message }
func (e *validationError) ErrorCode() string    { return e.code }
func (e *validationError) ErrorMessage() string { return e.message }
func (e *validationError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultClient
}

var _ smithy.APIError = (*validationError)(nil)

// TestDynamoDBMemory_ItemTooLarge verifies that a ValidationException with the size message
// is wrapped with "dynamodb conversation: item too large".
// Req 12.2, 12.3
func TestDynamoDBMemory_ItemTooLarge(t *testing.T) {
	mock := newMockDynamoDBClient()
	mock.putErr = &validationError{
		code:    "ValidationException",
		message: "Item size has exceeded the maximum allowed size",
	}

	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "dynamodb conversation: item too large") {
		t.Errorf("expected error to contain %q, got %q", "dynamodb conversation: item too large", err.Error())
	}
}

// TestDynamoDBMemory_OtherValidationException verifies that a ValidationException with a different
// message is wrapped with "dynamodb conversation: save" (not item-too-large).
// Req 12.3
func TestDynamoDBMemory_OtherValidationException(t *testing.T) {
	mock := newMockDynamoDBClient()
	mock.putErr = &validationError{
		code:    "ValidationException",
		message: "One or more parameter values were invalid",
	}

	m := &DynamoDBConversation{
		client:       mock,
		table:        "test-table",
		keyPrefix:    "gude:",
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}

	err := m.Save(context.Background(), "conv1", []agent.Message{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "item too large") {
		t.Errorf("expected generic save error, but got item-too-large: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "dynamodb conversation: save") {
		t.Errorf("expected error to contain %q, got %q", "dynamodb conversation: save", err.Error())
	}
}
