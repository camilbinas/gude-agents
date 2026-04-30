package memory

import (
	"context"
	"testing"
)

// testEntry is a minimal struct for testing.
type testEntry struct {
	ID      string  `json:"id" db:"id,pk"`
	UserID  string  `json:"user_id" db:"user_id,identifier"`
	Content string  `json:"content" db:"content,content" description:"The content" required:"true"`
	Score   float64 `json:"score" db:"score" description:"A numeric score"`
}

// mockEmbedder returns a fixed embedding for any input.
type mockEmbedder struct {
	embeddings map[string][]float64
	dim        int
}

func newMockEmbedder(dim int) *mockEmbedder {
	return &mockEmbedder{embeddings: make(map[string][]float64), dim: dim}
}

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	if emb, ok := m.embeddings[text]; ok {
		return emb, nil
	}
	// Generate a deterministic embedding based on text length.
	emb := make([]float64, m.dim)
	for i := range emb {
		emb[i] = float64((len(text)+i)%10) / 10.0
	}
	return emb, nil
}

// --- Interface compliance ---

var _ Memory[testEntry] = (*Store[testEntry])(nil)
var _ Updater[testEntry] = (*Store[testEntry])(nil)
var _ Forgetter[testEntry] = (*Store[testEntry])(nil)
var _ BulkForgetter[testEntry] = (*Store[testEntry])(nil)

// --- Tests ---

func TestStore_Update(t *testing.T) {
	embedder := newMockEmbedder(4)
	store, err := NewStore[testEntry](embedder)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Remember an entry.
	err = store.Remember(ctx, "user-1", testEntry{Content: "I like Go", Score: 0.8})
	if err != nil {
		t.Fatal(err)
	}

	// Recall to get the ID.
	results, err := store.Recall(ctx, "user-1", "Go", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	entryID := results[0].ID

	// Update the entry.
	err = store.Update(ctx, "user-1", entryID, testEntry{Content: "I like Rust now", Score: 0.9})
	if err != nil {
		t.Fatal(err)
	}

	// Recall again — should get updated content.
	results, err = store.Recall(ctx, "user-1", "Rust", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Value.Content != "I like Rust now" {
		t.Errorf("expected updated content, got %q", results[0].Value.Content)
	}
	if results[0].Value.Score != 0.9 {
		t.Errorf("expected score 0.9, got %f", results[0].Value.Score)
	}
	if results[0].ID != entryID {
		t.Errorf("expected same ID %q, got %q", entryID, results[0].ID)
	}
}

func TestStore_Update_NotFound(t *testing.T) {
	embedder := newMockEmbedder(4)
	store, err := NewStore[testEntry](embedder)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	err = store.Update(ctx, "user-1", "nonexistent-id", testEntry{Content: "hello"})
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestStore_Update_EmptyIdentifier(t *testing.T) {
	embedder := newMockEmbedder(4)
	store, err := NewStore[testEntry](embedder)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	err = store.Update(ctx, "", "some-id", testEntry{Content: "hello"})
	if err == nil {
		t.Fatal("expected error for empty identifier")
	}
}

func TestStore_Update_EmptyID(t *testing.T) {
	embedder := newMockEmbedder(4)
	store, err := NewStore[testEntry](embedder)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	err = store.Update(ctx, "user-1", "", testEntry{Content: "hello"})
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestStore_Forget(t *testing.T) {
	embedder := newMockEmbedder(4)
	store, err := NewStore[testEntry](embedder)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	_ = store.Remember(ctx, "user-1", testEntry{Content: "entry one"})
	_ = store.Remember(ctx, "user-1", testEntry{Content: "entry two"})

	results, _ := store.Recall(ctx, "user-1", "entry", 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Forget the first one.
	err = store.Forget(ctx, "user-1", results[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	results, _ = store.Recall(ctx, "user-1", "entry", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result after forget, got %d", len(results))
	}
}

func TestStore_ForgetAll(t *testing.T) {
	embedder := newMockEmbedder(4)
	store, err := NewStore[testEntry](embedder)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	_ = store.Remember(ctx, "user-1", testEntry{Content: "entry one"})
	_ = store.Remember(ctx, "user-1", testEntry{Content: "entry two"})

	err = store.ForgetAll(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}

	results, _ := store.Recall(ctx, "user-1", "entry", 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results after forget all, got %d", len(results))
	}
}

func TestNewUpdateTool(t *testing.T) {
	embedder := newMockEmbedder(4)
	store, err := NewStore[testEntry](embedder)
	if err != nil {
		t.Fatal(err)
	}

	tl := NewUpdateTool(store, WithToolName("update_entry"))
	if tl.Spec.Name != "update_entry" {
		t.Errorf("expected tool name 'update_entry', got %q", tl.Spec.Name)
	}

	// Verify the schema includes "id" in properties.
	props, ok := tl.Spec.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in schema")
	}
	if _, ok := props["id"]; !ok {
		t.Error("expected 'id' in schema properties")
	}
}
