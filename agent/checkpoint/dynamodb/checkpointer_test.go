package dynamodb

import (
	"context"
	"errors"
	"strings"
	"sync"
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

func TestSaveIfVersion_RejectsInvalidInput(t *testing.T) {
	c := newFakeCheckpointer()
	ctx := context.Background()

	if _, err := c.SaveIfVersion(ctx, "", checkpoint.Checkpoint{}, 0); !errors.Is(err, checkpoint.ErrThreadIDRequired) {
		t.Errorf("empty thread ID error = %v, want ErrThreadIDRequired", err)
	}
	if _, err := c.SaveIfVersion(ctx, "t1", checkpoint.Checkpoint{}, -1); err == nil || !strings.Contains(err.Error(), "expected version must be non-negative") {
		t.Errorf("negative expected version error = %v, want explanatory error", err)
	}
}

func TestSaveIfVersion_CreateExpectedZero(t *testing.T) {
	c := newFakeCheckpointer()

	got, err := c.SaveIfVersion(context.Background(), "t1", checkpoint.Checkpoint{Label: "created"}, 0)
	if err != nil {
		t.Fatalf("save if version: %v", err)
	}
	if got.ThreadID != "t1" || got.Version != 1 || got.Label != "created" {
		t.Errorf("checkpoint = %+v, want thread t1 at version 1", got)
	}
	if got.Timestamp.IsZero() {
		t.Error("timestamp not defaulted")
	}
}

func TestSaveIfVersion_SucceedsAtCurrentVersion(t *testing.T) {
	c := newFakeCheckpointer()
	ctx := context.Background()

	if _, err := c.Save(ctx, "t1", checkpoint.Checkpoint{}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	got, err := c.SaveIfVersion(ctx, "t1", checkpoint.Checkpoint{Label: "updated"}, 1)
	if err != nil {
		t.Fatalf("save if version: %v", err)
	}
	if got.Version != 2 || got.Label != "updated" {
		t.Errorf("checkpoint = %+v, want version 2 with updated label", got)
	}
}

func TestSaveIfVersion_RejectsVersionMismatchWithoutWriting(t *testing.T) {
	tests := []struct {
		name            string
		seed            bool
		expectedVersion int
		currentVersion  string
	}{
		{name: "stale expectation", seed: true, expectedVersion: 0, currentVersion: "current version 1"},
		{name: "absent expected nonzero", expectedVersion: 1, currentVersion: "current version 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeDynamo()
			c := &Checkpointer{client: fake, table: "checkpoints"}
			ctx := context.Background()
			if tt.seed {
				if _, err := c.Save(ctx, "t1", checkpoint.Checkpoint{}); err != nil {
					t.Fatalf("seed save: %v", err)
				}
			}
			putCalls := fake.putCalls

			_, err := c.SaveIfVersion(ctx, "t1", checkpoint.Checkpoint{}, tt.expectedVersion)
			if !errors.Is(err, checkpoint.ErrConflict) {
				t.Fatalf("error = %v, want ErrConflict", err)
			}
			if !strings.Contains(err.Error(), tt.currentVersion) {
				t.Errorf("error = %v, want %q", err, tt.currentVersion)
			}
			if fake.putCalls != putCalls {
				t.Errorf("PutItem calls = %d, want %d", fake.putCalls, putCalls)
			}
		})
	}
}

func TestSaveIfVersion_ConcurrentOneWinner(t *testing.T) {
	c := newFakeCheckpointer()
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup

	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := c.SaveIfVersion(ctx, "t1", checkpoint.Checkpoint{}, 0)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, checkpoint.ErrConflict):
			conflicts++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Errorf("successes = %d, conflicts = %d; want 1 each", successes, conflicts)
	}
}

func TestSaveIfVersion_MapsConditionalFailureWithoutRetry(t *testing.T) {
	message := "forced conditional failure"
	fake := newFakeDynamo()
	fake.putErr = &dbtypes.ConditionalCheckFailedException{Message: &message}
	c := &Checkpointer{client: fake, table: "checkpoints"}

	_, err := c.SaveIfVersion(context.Background(), "t1", checkpoint.Checkpoint{}, 0)
	if !errors.Is(err, checkpoint.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	if fake.putCalls != 1 {
		t.Errorf("PutItem calls = %d, want 1", fake.putCalls)
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
