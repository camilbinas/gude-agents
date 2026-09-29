package dynamodb

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

func genMessages(t *rapid.T) []agent.Message { return testutil.GenMessages(t, 10) }

func TestProperty_DynamoDBSaveLoadCAS(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := testStore(newMockDynamoDBClient())
		id := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "id")
		first := genMessages(t)
		second := genMessages(t)
		ctx := context.Background()
		rev, err := m.Save(ctx, id, first, 0)
		if err != nil || rev != 1 {
			t.Fatalf("first Save = %d, %v", rev, err)
		}
		rev, err = m.Save(ctx, id, second, rev)
		if err != nil || rev != 2 {
			t.Fatalf("second Save = %d, %v", rev, err)
		}
		if _, err := m.Save(ctx, id, first, 1); !errors.Is(err, agent.ErrConversationConflict) {
			t.Fatalf("stale Save = %v", err)
		}
		snapshot, err := m.Load(ctx, id)
		if err != nil || snapshot.Revision != 2 || !reflect.DeepEqual(snapshot.Messages, second) {
			t.Fatalf("Load = %+v, %v", snapshot, err)
		}
	})
}

func TestProperty_DynamoDBDeleteThenLoad(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := testStore(newMockDynamoDBClient())
		id := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "id")
		ctx := context.Background()
		if _, err := m.Save(ctx, id, genMessages(t), 0); err != nil {
			t.Fatal(err)
		}
		if err := m.Delete(ctx, id); err != nil {
			t.Fatal(err)
		}
		snapshot, err := m.Load(ctx, id)
		if err != nil || snapshot.Messages == nil || snapshot.Revision != 0 || len(snapshot.Messages) != 0 {
			t.Fatalf("Load after delete = %+v, %v", snapshot, err)
		}
	})
}
