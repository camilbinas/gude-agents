//go:build integration

package dynamodb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/camilbinas/gude-agents/agent/graph"
)

// skipIfNoDynamoDB skips the test if DYNAMODB_ENDPOINT is not set.
func skipIfNoDynamoDB(t *testing.T) string {
	t.Helper()
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set, skipping DynamoDB integration test")
	}
	return endpoint
}

// newTestCheckpointer creates a Checkpointer with a unique table and registers cleanup.
func newTestCheckpointer(t *testing.T) *Checkpointer {
	t.Helper()
	endpoint := skipIfNoDynamoDB(t)
	table := fmt.Sprintf("graph_checkpoints_test_%d", os.Getpid())

	cfg := aws.Config{
		Region: "us-east-1",
	}

	cp, err := New(cfg, table, WithEndpoint(endpoint))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Create the table for testing.
	client := awsdynamodb.NewFromConfig(cfg, func(o *awsdynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	_, err = client.CreateTable(context.Background(), &awsdynamodb.CreateTableInput{
		TableName: aws.String(table),
		KeySchema: []dbtypes.KeySchemaElement{
			{AttributeName: aws.String("thread_id"), KeyType: dbtypes.KeyTypeHash},
			{AttributeName: aws.String("version"), KeyType: dbtypes.KeyTypeRange},
		},
		AttributeDefinitions: []dbtypes.AttributeDefinition{
			{AttributeName: aws.String("thread_id"), AttributeType: dbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("version"), AttributeType: dbtypes.ScalarAttributeTypeN},
		},
		BillingMode: dbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	t.Cleanup(func() {
		client.DeleteTable(context.Background(), &awsdynamodb.DeleteTableInput{
			TableName: aws.String(table),
		})
	})

	return cp
}

func TestNew_EmptyTable(t *testing.T) {
	_, err := New(aws.Config{}, "")
	if err == nil {
		t.Fatal("expected error for empty table, got nil")
	}
}

func TestSaveAndLoad(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	checkpoint := graph.Checkpoint{
		State:      graph.State{"key": "value", "count": float64(42)},
		Completed:  map[string]bool{"node_a": true},
		Iterations: 1,
		NodeName:   "node_a",
		Timestamp:  time.Now().Truncate(time.Millisecond),
	}

	saved, err := cp.Save(ctx, "thread-1", checkpoint)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Version != 1 {
		t.Fatalf("expected version 1, got %d", saved.Version)
	}
	if saved.ThreadID != "thread-1" {
		t.Fatalf("expected thread_id 'thread-1', got %q", saved.ThreadID)
	}

	loaded, err := cp.Load(ctx, "thread-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != 1 {
		t.Fatalf("expected version 1, got %d", loaded.Version)
	}
	if loaded.NodeName != "node_a" {
		t.Fatalf("expected node_name 'node_a', got %q", loaded.NodeName)
	}
	if loaded.State["key"] != "value" {
		t.Fatalf("expected state key 'value', got %v", loaded.State["key"])
	}
}

func TestLoad_NotFound(t *testing.T) {
	cp := newTestCheckpointer(t)

	_, err := cp.Load(context.Background(), "nonexistent")
	if err != graph.ErrCheckpointNotFound {
		t.Fatalf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestLoadAt(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	// Save multiple versions.
	for i := 0; i < 3; i++ {
		_, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			State:     graph.State{"step": float64(i)},
			NodeName:  fmt.Sprintf("node_%d", i),
			Timestamp: time.Now().Truncate(time.Millisecond),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	// Load version 2.
	loaded, err := cp.LoadAt(ctx, "thread-1", 2)
	if err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if loaded.Version != 2 {
		t.Fatalf("expected version 2, got %d", loaded.Version)
	}
	if loaded.NodeName != "node_1" {
		t.Fatalf("expected node_name 'node_1', got %q", loaded.NodeName)
	}
}

func TestLoadAt_NotFound(t *testing.T) {
	cp := newTestCheckpointer(t)

	_, err := cp.LoadAt(context.Background(), "thread-1", 99)
	if err != graph.ErrCheckpointNotFound {
		t.Fatalf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestHistory(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			NodeName:  fmt.Sprintf("node_%d", i),
			Timestamp: time.Now().Truncate(time.Millisecond),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	metas, err := cp.History(ctx, "thread-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(metas) != 5 {
		t.Fatalf("expected 5 history entries, got %d", len(metas))
	}

	// Verify ordering (ascending).
	for i, m := range metas {
		expectedVersion := i + 1
		if m.Version != expectedVersion {
			t.Errorf("entry %d: expected version %d, got %d", i, expectedVersion, m.Version)
		}
		expectedNode := fmt.Sprintf("node_%d", i)
		if m.NodeName != expectedNode {
			t.Errorf("entry %d: expected node %q, got %q", i, expectedNode, m.NodeName)
		}
	}
}

func TestList(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	threads := []string{"thread-a", "thread-b", "thread-c"}
	for _, tid := range threads {
		_, err := cp.Save(ctx, tid, graph.Checkpoint{
			NodeName:  "node_0",
			Timestamp: time.Now().Truncate(time.Millisecond),
		})
		if err != nil {
			t.Fatalf("Save %s: %v", tid, err)
		}
	}

	ids, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	for _, tid := range threads {
		if !idSet[tid] {
			t.Errorf("expected thread %q in list, not found", tid)
		}
	}
}

func TestDelete(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	// Save multiple versions.
	for i := 0; i < 3; i++ {
		_, err := cp.Save(ctx, "thread-del", graph.Checkpoint{
			NodeName:  fmt.Sprintf("node_%d", i),
			Timestamp: time.Now().Truncate(time.Millisecond),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	// Delete all.
	if err := cp.Delete(ctx, "thread-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify Load returns not found.
	_, err := cp.Load(ctx, "thread-del")
	if err != graph.ErrCheckpointNotFound {
		t.Fatalf("expected ErrCheckpointNotFound after delete, got %v", err)
	}

	// Verify List does not include deleted thread.
	ids, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, id := range ids {
		if id == "thread-del" {
			t.Fatal("deleted thread still appears in List")
		}
	}
}

func TestKeyPrefix(t *testing.T) {
	endpoint := skipIfNoDynamoDB(t)
	table := fmt.Sprintf("graph_checkpoints_prefix_%d", os.Getpid())

	cfg := aws.Config{
		Region: "us-east-1",
	}

	cp, err := New(cfg, table, WithEndpoint(endpoint), WithKeyPrefix("myapp:"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Create the table.
	client := awsdynamodb.NewFromConfig(cfg, func(o *awsdynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	_, err = client.CreateTable(context.Background(), &awsdynamodb.CreateTableInput{
		TableName: aws.String(table),
		KeySchema: []dbtypes.KeySchemaElement{
			{AttributeName: aws.String("thread_id"), KeyType: dbtypes.KeyTypeHash},
			{AttributeName: aws.String("version"), KeyType: dbtypes.KeyTypeRange},
		},
		AttributeDefinitions: []dbtypes.AttributeDefinition{
			{AttributeName: aws.String("thread_id"), AttributeType: dbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("version"), AttributeType: dbtypes.ScalarAttributeTypeN},
		},
		BillingMode: dbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	t.Cleanup(func() {
		client.DeleteTable(context.Background(), &awsdynamodb.DeleteTableInput{
			TableName: aws.String(table),
		})
	})

	ctx := context.Background()
	_, err = cp.Save(ctx, "thread-1", graph.Checkpoint{
		NodeName:  "node_a",
		Timestamp: time.Now().Truncate(time.Millisecond),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := cp.Load(ctx, "thread-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.NodeName != "node_a" {
		t.Fatalf("expected node_name 'node_a', got %q", loaded.NodeName)
	}
}

func TestLoadReturnsLatest(t *testing.T) {
	cp := newTestCheckpointer(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := cp.Save(ctx, "thread-1", graph.Checkpoint{
			NodeName:  fmt.Sprintf("node_%d", i),
			Timestamp: time.Now().Truncate(time.Millisecond),
		})
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	loaded, err := cp.Load(ctx, "thread-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != 5 {
		t.Fatalf("expected latest version 5, got %d", loaded.Version)
	}
	if loaded.NodeName != "node_4" {
		t.Fatalf("expected node_name 'node_4', got %q", loaded.NodeName)
	}
}
