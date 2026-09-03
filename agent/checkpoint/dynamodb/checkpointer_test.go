package dynamodb

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
)

// newFakeCheckpointer builds a Checkpointer over the in-memory fake client.
func newFakeCheckpointer() *Checkpointer {
	return &Checkpointer{client: newFakeDynamo(), table: "checkpoints"}
}

// Runs the shared contract against the fake client. This verifies version
// assignment, key conditions, ordering, and Extra round-tripping through the
// single JSON `data` attribute without needing AWS.
func TestConformance_Fake(t *testing.T) {
	checkpoint.RunConformance(t, func(t *testing.T) checkpoint.Checkpointer {
		return newFakeCheckpointer()
	})
}

func TestNew_RequiresTableName(t *testing.T) {
	if _, err := New(aws.Config{}, ""); err == nil {
		t.Error("expected error for empty table name, got nil")
	}
}

func TestSave_RejectsEmptyThreadID(t *testing.T) {
	c := newFakeCheckpointer()
	if _, err := c.Save(context.Background(), "", checkpoint.Checkpoint{}); !errors.Is(err, checkpoint.ErrThreadIDRequired) {
		t.Errorf("err = %v, want ErrThreadIDRequired", err)
	}
}

// Save must default Timestamp consistently across backends.
func TestSave_DefaultsTimestamp(t *testing.T) {
	c := newFakeCheckpointer()
	got, err := c.Save(context.Background(), "t1", checkpoint.Checkpoint{})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if got.Timestamp.IsZero() {
		t.Error("timestamp not defaulted")
	}
}

func TestKeyPrefix_IsCollisionSafeAndStrippedFromList(t *testing.T) {
	fake := newFakeDynamo()
	c := &Checkpointer{client: fake, table: "checkpoints", keyPrefix: "app"}
	foreign := &Checkpointer{client: fake, table: "checkpoints", keyPrefix: "app2:"}
	ctx := context.Background()

	if _, err := c.Save(ctx, "2:thread-1", checkpoint.Checkpoint{}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := foreign.Save(ctx, "thread-2", checkpoint.Checkpoint{}); err != nil {
		t.Fatalf("save foreign namespace: %v", err)
	}

	if _, ok := fake.items["3:app2:thread-1"]; !ok {
		t.Errorf("expected encoded partition key, got keys %v", keysOf(fake))
	}

	ids, err := c.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 1 || ids[0] != "2:thread-1" {
		t.Errorf("List() = %v, want [2:thread-1]", ids)
	}

	if _, err := c.Load(ctx, "2:thread-1"); err != nil {
		t.Errorf("load: %v", err)
	}
}

func TestDelete_RemovesEveryVersion(t *testing.T) {
	fake := newFakeDynamo()
	c := &Checkpointer{client: fake, table: "checkpoints"}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := c.Save(ctx, "t1", checkpoint.Checkpoint{}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	if err := c.Delete(ctx, "t1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if fake.delCalls != 3 {
		t.Errorf("DeleteItem calls = %d, want 3", fake.delCalls)
	}
	if _, err := c.Load(ctx, "t1"); !errors.Is(err, checkpoint.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUnmarshalItem_RejectsMalformedItems(t *testing.T) {
	c := newFakeCheckpointer()

	if _, err := c.unmarshalItem(map[string]dbtypes.AttributeValue{}); err == nil {
		t.Error("expected error for item missing 'data', got nil")
	}
}

func keysOf(f *fakeDynamo) []string {
	out := make([]string, 0, len(f.items))
	for k := range f.items {
		out = append(out, k)
	}
	return out
}
