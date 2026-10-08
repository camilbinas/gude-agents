package dynamodb

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

type scriptedDynamo struct {
	getFn      func(context.Context, *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error)
	queryFn    func(context.Context, *dynamodb.QueryInput) (*dynamodb.QueryOutput, error)
	scanFn     func(context.Context, *dynamodb.ScanInput) (*dynamodb.ScanOutput, error)
	batchFn    func(context.Context, *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error)
	transactFn func(context.Context, *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error)
	updateFn   func(context.Context, *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error)
}

func (s *scriptedDynamo) GetItem(ctx context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if s.getFn == nil {
		return nil, errors.New("unexpected GetItem")
	}
	return s.getFn(ctx, in)
}
func (s *scriptedDynamo) Query(ctx context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if s.queryFn == nil {
		return nil, errors.New("unexpected Query")
	}
	return s.queryFn(ctx, in)
}
func (s *scriptedDynamo) Scan(ctx context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if s.scanFn == nil {
		return nil, errors.New("unexpected Scan")
	}
	return s.scanFn(ctx, in)
}
func (s *scriptedDynamo) BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	if s.batchFn == nil {
		return nil, errors.New("unexpected BatchWriteItem")
	}
	return s.batchFn(ctx, in)
}
func (s *scriptedDynamo) TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	if s.transactFn == nil {
		return nil, errors.New("unexpected TransactWriteItems")
	}
	return s.transactFn(ctx, in)
}
func (s *scriptedDynamo) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if s.updateFn == nil {
		return nil, errors.New("unexpected UpdateItem")
	}
	return s.updateFn(ctx, in)
}

func testConversation(client dynamoDBClient) *Conversation {
	return &Conversation{
		client:              client,
		table:               "conversations",
		keyPrefix:           "gude:",
		ttlAttribute:        "ttl",
		pkAttribute:         "conversation_id",
		skAttribute:         "sequence",
		waitBatchWriteRetry: func(context.Context, int) error { return nil },
	}
}

func metadataItem(revision, last uint64) map[string]dbtypes.AttributeValue {
	return map[string]dbtypes.AttributeValue{"revision": avn(revision), "last_sequence": avn(last)}
}

func eventItem(t *testing.T, store *Conversation, id string, sequence uint64, text string) map[string]dbtypes.AttributeValue {
	t.Helper()
	raw, err := conversation.MarshalMessages([]agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}})
	if err != nil {
		t.Fatal(err)
	}
	item := store.key(id, msgKey(sequence))
	item["message"] = avs(string(raw))
	return item
}

func textOf(t *testing.T, msg agent.Message) string {
	t.Helper()
	text, ok := msg.Content[0].(agent.TextBlock)
	if !ok {
		t.Fatalf("message content = %#v", msg.Content)
	}
	return text.Text
}

func TestLoadAfterPaginatesAndPreservesCursor(t *testing.T) {
	id := "conversation"
	pageKey := map[string]dbtypes.AttributeValue{"conversation_id": avs("next"), "sequence": avs("MSG#00000000000000000002")}
	calls := 0
	client := &scriptedDynamo{}
	store := testConversation(client)
	client.getFn = func(context.Context, *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
		return &dynamodb.GetItemOutput{Item: metadataItem(7, 3)}, nil
	}
	client.queryFn = func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		calls++
		if calls == 1 {
			if in.ExclusiveStartKey != nil {
				t.Fatalf("first page start key = %#v", in.ExclusiveStartKey)
			}
			return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{eventItem(t, store, id, 1, "one"), eventItem(t, store, id, 2, "two")}, LastEvaluatedKey: pageKey}, nil
		}
		if !reflect.DeepEqual(in.ExclusiveStartKey, pageKey) {
			t.Fatalf("second page start key = %#v, want %#v", in.ExclusiveStartKey, pageKey)
		}
		return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{eventItem(t, store, id, 3, "three")}}, nil
	}

	snapshot, err := store.LoadAfter(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || snapshot.Revision != 7 || snapshot.LastSequence != 3 {
		t.Fatalf("calls=%d snapshot=%+v", calls, snapshot)
	}
	if got := []string{textOf(t, snapshot.Messages[0]), textOf(t, snapshot.Messages[1]), textOf(t, snapshot.Messages[2])}; !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Fatalf("messages = %#v", got)
	}
}

func TestLoadAfterStartsWithinMultiPageConversation(t *testing.T) {
	id := "conversation"
	pageKey := map[string]dbtypes.AttributeValue{"conversation_id": avs("next"), "sequence": avs("MSG#00000000000000000002")}
	calls := 0
	client := &scriptedDynamo{}
	store := testConversation(client)
	client.getFn = func(context.Context, *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
		return &dynamodb.GetItemOutput{Item: metadataItem(4, 4)}, nil
	}
	client.queryFn = func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		calls++
		if got := in.ExpressionAttributeValues[":after"].(*dbtypes.AttributeValueMemberS).Value; got != msgKey(2) {
			t.Fatalf("after key = %q, want %q", got, msgKey(2))
		}
		if calls == 1 {
			return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{eventItem(t, store, id, 3, "three")}, LastEvaluatedKey: pageKey}, nil
		}
		return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{eventItem(t, store, id, 4, "four")}}, nil
	}

	snapshot, err := store.LoadAfter(context.Background(), id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(snapshot.Messages) != 2 || textOf(t, snapshot.Messages[0]) != "three" || textOf(t, snapshot.Messages[1]) != "four" || snapshot.Revision != 4 || snapshot.LastSequence != 4 {
		t.Fatalf("calls=%d snapshot=%+v", calls, snapshot)
	}
}

func TestLoadAfterReturnsErrorOnLaterPageFailure(t *testing.T) {
	id := "conversation"
	pageErr := errors.New("page two failed")
	client := &scriptedDynamo{}
	store := testConversation(client)
	client.getFn = func(context.Context, *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
		return &dynamodb.GetItemOutput{Item: metadataItem(2, 2)}, nil
	}
	queries := 0
	client.queryFn = func(context.Context, *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		queries++
		if queries == 1 {
			return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{eventItem(t, store, id, 1, "one")}, LastEvaluatedKey: map[string]dbtypes.AttributeValue{"sequence": avs("next")}}, nil
		}
		return nil, pageErr
	}

	_, err := store.LoadAfter(context.Background(), id, 0)
	if !errors.Is(err, pageErr) {
		t.Fatalf("LoadAfter error = %v, want page-two error", err)
	}
}

func TestLoadAfterCancellationStopsBeforeNextPage(t *testing.T) {
	id := "conversation"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &scriptedDynamo{}
	store := testConversation(client)
	client.getFn = func(context.Context, *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
		return &dynamodb.GetItemOutput{Item: metadataItem(1, 1)}, nil
	}
	queries := 0
	client.queryFn = func(context.Context, *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		queries++
		cancel()
		return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{eventItem(t, store, id, 1, "one")}, LastEvaluatedKey: map[string]dbtypes.AttributeValue{"sequence": avs("next")}}, nil
	}

	_, err := store.LoadAfter(ctx, id, 0)
	if !errors.Is(err, context.Canceled) || queries != 1 {
		t.Fatalf("LoadAfter error=%v queries=%d", err, queries)
	}
}

func TestDeletePaginatesBatchesAndRetriesUnprocessedItems(t *testing.T) {
	id := "conversation"
	pageKey := map[string]dbtypes.AttributeValue{"conversation_id": avs("next"), "sequence": avs("MSG#00000000000000000029")}
	client := &scriptedDynamo{}
	store := testConversation(client)

	allItems := make([]map[string]dbtypes.AttributeValue, 0, 52)
	meta := store.key(id, "META")
	meta["context_test"] = avs(`{"derived":true}`)
	allItems = append(allItems, meta)
	for sequence := uint64(1); sequence <= 51; sequence++ {
		allItems = append(allItems, store.key(id, msgKey(sequence)))
	}
	queries := 0
	client.queryFn = func(_ context.Context, in *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		queries++
		if queries == 1 {
			if in.ExclusiveStartKey != nil {
				t.Fatalf("first query start key = %#v", in.ExclusiveStartKey)
			}
			return &dynamodb.QueryOutput{Items: allItems[:30], LastEvaluatedKey: pageKey}, nil
		}
		if !reflect.DeepEqual(in.ExclusiveStartKey, pageKey) {
			t.Fatalf("second query start key = %#v, want %#v", in.ExclusiveStartKey, pageKey)
		}
		return &dynamodb.QueryOutput{Items: allItems[30:]}, nil
	}

	var batchSizes []int
	deleted := map[string]bool{}
	batchCalls := 0
	waits := 0
	store.waitBatchWriteRetry = func(context.Context, int) error {
		waits++
		return nil
	}
	client.batchFn = func(_ context.Context, in *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		batchCalls++
		requests := in.RequestItems[store.table]
		batchSizes = append(batchSizes, len(requests))
		for _, request := range requests {
			key := request.DeleteRequest.Key[store.skAttribute].(*dbtypes.AttributeValueMemberS).Value
			deleted[key] = true
		}
		if batchCalls == 1 {
			return &dynamodb.BatchWriteItemOutput{UnprocessedItems: map[string][]dbtypes.WriteRequest{store.table: requests[len(requests)-1:]}}, nil
		}
		return &dynamodb.BatchWriteItemOutput{}, nil
	}

	if err := store.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if queries != 2 || !reflect.DeepEqual(batchSizes, []int{25, 1, 25, 2}) || waits != 1 {
		t.Fatalf("queries=%d batch sizes=%v waits=%d", queries, batchSizes, waits)
	}
	if len(deleted) != len(allItems) || !deleted["META"] {
		t.Fatalf("deleted=%d, want all %d including META", len(deleted), len(allItems))
	}
}

func TestDeleteReturnsErrorBeforePartialDeletionOnLaterPageFailure(t *testing.T) {
	pageErr := errors.New("page two failed")
	client := &scriptedDynamo{}
	store := testConversation(client)
	queries := 0
	batchCalls := 0
	client.queryFn = func(context.Context, *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		queries++
		if queries == 1 {
			return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{store.key("conversation", "META")}, LastEvaluatedKey: map[string]dbtypes.AttributeValue{"sequence": avs("next")}}, nil
		}
		return nil, pageErr
	}
	client.batchFn = func(context.Context, *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		batchCalls++
		return &dynamodb.BatchWriteItemOutput{}, nil
	}

	err := store.Delete(context.Background(), "conversation")
	if !errors.Is(err, pageErr) || batchCalls != 0 {
		t.Fatalf("Delete error=%v batch calls=%d", err, batchCalls)
	}
}

func TestDeleteCancellationStopsUnprocessedRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &scriptedDynamo{}
	store := testConversation(client)
	client.queryFn = func(context.Context, *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{store.key("conversation", "META")}}, nil
	}
	batchCalls := 0
	client.batchFn = func(_ context.Context, in *dynamodb.BatchWriteItemInput) (*dynamodb.BatchWriteItemOutput, error) {
		batchCalls++
		return &dynamodb.BatchWriteItemOutput{UnprocessedItems: in.RequestItems}, nil
	}
	store.waitBatchWriteRetry = func(context.Context, int) error {
		cancel()
		return ctx.Err()
	}

	err := store.Delete(ctx, "conversation")
	if !errors.Is(err, context.Canceled) || batchCalls != 1 {
		t.Fatalf("Delete error=%v batch calls=%d", err, batchCalls)
	}
}

func TestLoadAfterReturnsErrorOnLaterPageMalformedData(t *testing.T) {
	id := "conversation"
	client := &scriptedDynamo{}
	store := testConversation(client)
	client.getFn = func(context.Context, *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
		return &dynamodb.GetItemOutput{Item: metadataItem(2, 2)}, nil
	}
	queries := 0
	client.queryFn = func(context.Context, *dynamodb.QueryInput) (*dynamodb.QueryOutput, error) {
		queries++
		if queries == 1 {
			return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{eventItem(t, store, id, 1, "one")}, LastEvaluatedKey: map[string]dbtypes.AttributeValue{"sequence": avs("next")}}, nil
		}
		return &dynamodb.QueryOutput{Items: []map[string]dbtypes.AttributeValue{{store.skAttribute: avs(msgKey(2))}}}, nil
	}

	_, err := store.LoadAfter(context.Background(), id, 0)
	if err == nil || queries != 2 {
		t.Fatalf("LoadAfter error=%v queries=%d", err, queries)
	}
}

func TestWriteExpressionsContainNoUnusedValues(t *testing.T) {
	client := &scriptedDynamo{}
	store := testConversation(client)
	client.getFn = func(context.Context, *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
		return &dynamodb.GetItemOutput{}, nil
	}
	client.transactFn = func(_ context.Context, in *dynamodb.TransactWriteItemsInput) (*dynamodb.TransactWriteItemsOutput, error) {
		values := in.TransactItems[0].Update.ExpressionAttributeValues
		if _, found := values[":zero"]; found {
			t.Fatalf("Append sent unused :zero expression value: %#v", values)
		}
		return &dynamodb.TransactWriteItemsOutput{}, nil
	}
	if _, err := store.Append(context.Background(), "conversation", []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "one"}}}}, 0); err != nil {
		t.Fatal(err)
	}
	client.updateFn = func(_ context.Context, in *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
		if _, found := in.ExpressionAttributeValues[":zero"]; found {
			t.Fatalf("SaveContextState sent unused :zero expression value: %#v", in.ExpressionAttributeValues)
		}
		return &dynamodb.UpdateItemOutput{}, nil
	}
	if _, err := store.SaveContextState(context.Background(), "conversation", "summary", []byte(`{"v":1}`), 0); err != nil {
		t.Fatal(err)
	}
}
