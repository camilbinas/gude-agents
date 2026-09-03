package dynamodb

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDynamo is an in-memory stand-in for the DynamoDB operations this package
// uses. It is deliberately narrow: it understands only the two key-condition
// shapes Checkpointer emits ("thread_id = :tid" and that plus "version = :ver"),
// ScanIndexForward, Limit, and a projection-only Scan.
//
// It does not emulate pagination — LastEvaluatedKey is always empty — so the
// pagination loops in History, List and Delete are exercised for their
// single-page path only. Real multi-page behaviour still needs a live table.
type fakeDynamo struct {
	mu    sync.Mutex
	items map[string]map[int]string

	putCalls int
	delCalls int
}

func newFakeDynamo() *fakeDynamo {
	return &fakeDynamo{items: make(map[string]map[int]string)}
}

func pkOf(in map[string]dbtypes.AttributeValue, key string) (string, bool) {
	sv, ok := in[key].(*dbtypes.AttributeValueMemberS)
	if !ok {
		return "", false
	}
	return sv.Value, true
}

func (f *fakeDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCalls++

	pk, ok := pkOf(in.Item, "thread_id")
	if !ok {
		return nil, fmt.Errorf("fake: PutItem missing thread_id")
	}
	nv, ok := in.Item["version"].(*dbtypes.AttributeValueMemberN)
	if !ok {
		return nil, fmt.Errorf("fake: PutItem missing version")
	}
	version, err := strconv.Atoi(nv.Value)
	if err != nil {
		return nil, fmt.Errorf("fake: PutItem bad version %q", nv.Value)
	}
	data, ok := in.Item["data"].(*dbtypes.AttributeValueMemberS)
	if !ok {
		return nil, fmt.Errorf("fake: PutItem missing data")
	}

	if f.items[pk] == nil {
		f.items[pk] = make(map[int]string)
	}
	if in.ConditionExpression != nil {
		if _, exists := f.items[pk][version]; exists {
			message := "conditional write conflict"
			return nil, &dbtypes.ConditionalCheckFailedException{Message: &message}
		}
	}
	f.items[pk][version] = data.Value
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDynamo) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if in.ConsistentRead == nil || !*in.ConsistentRead {
		return nil, fmt.Errorf("fake: Query must use consistent reads")
	}

	tid, ok := pkOf(in.ExpressionAttributeValues, ":tid")
	if !ok {
		return nil, fmt.Errorf("fake: Query missing :tid")
	}

	versions := make([]int, 0, len(f.items[tid]))
	for v := range f.items[tid] {
		versions = append(versions, v)
	}
	sort.Ints(versions)

	if in.ScanIndexForward != nil && !*in.ScanIndexForward {
		sort.Sort(sort.Reverse(sort.IntSlice(versions)))
	}

	if strings.Contains(*in.KeyConditionExpression, ":ver") {
		nv, ok := in.ExpressionAttributeValues[":ver"].(*dbtypes.AttributeValueMemberN)
		if !ok {
			return nil, fmt.Errorf("fake: Query missing :ver")
		}
		want, err := strconv.Atoi(nv.Value)
		if err != nil {
			return nil, fmt.Errorf("fake: Query bad :ver")
		}
		filtered := versions[:0:0]
		for _, v := range versions {
			if v == want {
				filtered = append(filtered, v)
			}
		}
		versions = filtered
	}

	if in.Limit != nil && int(*in.Limit) < len(versions) {
		versions = versions[:*in.Limit]
	}

	items := make([]map[string]dbtypes.AttributeValue, 0, len(versions))
	for _, v := range versions {
		items = append(items, map[string]dbtypes.AttributeValue{
			"thread_id": &dbtypes.AttributeValueMemberS{Value: tid},
			"version":   &dbtypes.AttributeValueMemberN{Value: strconv.Itoa(v)},
			"data":      &dbtypes.AttributeValueMemberS{Value: f.items[tid][v]},
		})
	}
	return &dynamodb.QueryOutput{Items: items}, nil
}

func (f *fakeDynamo) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delCalls++

	pk, ok := pkOf(in.Key, "thread_id")
	if !ok {
		return nil, fmt.Errorf("fake: DeleteItem missing thread_id")
	}
	nv, ok := in.Key["version"].(*dbtypes.AttributeValueMemberN)
	if !ok {
		return nil, fmt.Errorf("fake: DeleteItem missing version")
	}
	version, _ := strconv.Atoi(nv.Value)

	delete(f.items[pk], version)
	if len(f.items[pk]) == 0 {
		delete(f.items, pk)
	}
	return &dynamodb.DeleteItemOutput{}, nil
}

func (f *fakeDynamo) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if in.ConsistentRead == nil || !*in.ConsistentRead {
		return nil, fmt.Errorf("fake: Scan must use consistent reads")
	}

	var items []map[string]dbtypes.AttributeValue
	for pk, versions := range f.items {
		for range versions {
			items = append(items, map[string]dbtypes.AttributeValue{
				"thread_id": &dbtypes.AttributeValueMemberS{Value: pk},
			})
		}
	}
	return &dynamodb.ScanOutput{Items: items}, nil
}
