package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
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
	results, err := store.Recall(ctx, "user-1", RecallQuery{Text: "Go", Limit: 1})
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
	results, err = store.Recall(ctx, "user-1", RecallQuery{Text: "Rust", Limit: 1})
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

	results, _ := store.Recall(ctx, "user-1", RecallQuery{Text: "entry", Limit: 10})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Forget the first one.
	err = store.Forget(ctx, "user-1", results[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	results, _ = store.Recall(ctx, "user-1", RecallQuery{Text: "entry", Limit: 10})
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

	results, _ := store.Recall(ctx, "user-1", RecallQuery{Text: "entry", Limit: 10})
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

func TestStoreRecallConcurrentWithUpdate(t *testing.T) {
	store, err := NewStore[testEntry](newMockEmbedder(4))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Remember(ctx, "user-1", testEntry{ID: "entry-1", Content: "initial"}); err != nil {
		t.Fatal(err)
	}

	const iterations = 1000
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := store.Update(ctx, "user-1", "entry-1", testEntry{ID: "entry-1", Content: "updated"}); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			entries, err := store.Recall(ctx, "user-1", RecallQuery{Text: "query", Limit: 1})
			if err != nil {
				errs <- err
				return
			}
			if len(entries) != 1 || entries[0].ID != "entry-1" {
				errs <- fmt.Errorf("unexpected recall result: %#v", entries)
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
func TestParseMemSchemaRejectsPointerGenericType(t *testing.T) {
	if _, err := parseMemSchema[testEntry](); err != nil {
		t.Fatalf("value type must remain supported: %v", err)
	}

	_, err := parseMemSchema[*testEntry]()
	if err == nil {
		t.Fatal("expected pointer generic type to be rejected")
	}
	if !strings.Contains(err.Error(), "T must be a non-pointer struct") || !strings.Contains(err.Error(), "*memory.testEntry") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveIdentity(t *testing.T) {
	cases := []struct {
		name    string
		ctx     context.Context
		scope   string
		want    string
		wantErr bool
	}{
		{"identity", agent.Background().WithIdentity("user-1"), "", "user-1", false},
		{"no identity", agent.Background(), "", "", true},
		{"plain context", context.Background(), "", "", true},
		{"scope set", agent.Background().WithIdentity("user-1").WithScope("tenant", "t-9"), "tenant", "t-9", false},
		// Strict scopes: a configured scope never falls back to the identity.
		{"scope missing no fallback", agent.Background().WithIdentity("user-1"), "tenant", "", true},
		{"scope empty value", agent.Background().WithScope("tenant", ""), "tenant", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveIdentity(tc.ctx, tc.scope)
			if tc.wantErr {
				if !errors.Is(err, ErrMissingIdentity) {
					t.Fatalf("err = %v, want ErrMissingIdentity", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTools_UseIdentityAndStrictScope(t *testing.T) {
	store, err := NewStore[testEntry](newMockEmbedder(4))
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"content":"likes Go"}`)

	// Without identity the tool fails explicitly.
	if _, err := NewRememberTool(store).Handler(agent.Background(), input); !errors.Is(err, ErrMissingIdentity) {
		t.Fatalf("remember without identity: err = %v, want ErrMissingIdentity", err)
	}

	// Default: partitioned by Identity.
	if _, err := NewRememberTool(store).Handler(agent.Background().WithIdentity("user-1"), input); err != nil {
		t.Fatalf("remember with identity: %v", err)
	}
	got, err := store.Recall(context.Background(), "user-1", RecallQuery{Text: "likes Go", Limit: 5})
	if err != nil || len(got) != 1 {
		t.Fatalf("recall user-1: %v, %d entries", err, len(got))
	}

	// Scoped tool with only an identity must not fall back to it.
	scoped := NewRememberTool(store, WithScope("tenant"))
	if _, err := scoped.Handler(agent.Background().WithIdentity("user-1"), input); !errors.Is(err, ErrMissingIdentity) {
		t.Fatalf("scoped remember without scope: err = %v, want ErrMissingIdentity", err)
	}

	// Scoped tool uses the scope value.
	if _, err := scoped.Handler(agent.Background().WithIdentity("user-1").WithScope("tenant", "t-9"), input); err != nil {
		t.Fatalf("scoped remember: %v", err)
	}
	got, err = store.Recall(context.Background(), "t-9", RecallQuery{Text: "likes Go", Limit: 5})
	if err != nil || len(got) != 1 {
		t.Fatalf("recall t-9: %v, %d entries", err, len(got))
	}
	if got, _ := store.Recall(context.Background(), "user-1", RecallQuery{Text: "likes Go", Limit: 5}); len(got) != 1 {
		t.Errorf("user-1 has %d entries, want 1 (scope write must not land on identity)", len(got))
	}

	recall := NewRecallTool(store, WithScope("tenant"))
	if _, err := recall.Handler(agent.Background(), json.RawMessage(`{"query":"x"}`)); !errors.Is(err, ErrMissingIdentity) {
		t.Errorf("recall without scope: err = %v, want ErrMissingIdentity", err)
	}
	forget := NewForgetTool(store)
	if _, err := forget.Handler(agent.Background(), json.RawMessage(`{"id":"x"}`)); !errors.Is(err, ErrMissingIdentity) {
		t.Errorf("forget without identity: err = %v, want ErrMissingIdentity", err)
	}
	update := NewUpdateTool(store)
	if _, err := update.Handler(agent.Background(), json.RawMessage(`{"id":"x","content":"y"}`)); !errors.Is(err, ErrMissingIdentity) {
		t.Errorf("update without identity: err = %v, want ErrMissingIdentity", err)
	}
}

func TestStore_RecallQuerySemantics(t *testing.T) {
	embedder := newMockEmbedder(2)
	embedder.embeddings["exact"] = []float64{1, 0}
	embedder.embeddings["weak"] = []float64{0.5, 0.8660254037844386}
	embedder.embeddings["opposite"] = []float64{-1, 0}
	embedder.embeddings["query"] = []float64{1, 0}
	store, err := NewStore[testEntry](embedder)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, content := range []string{"exact", "weak", "opposite"} {
		if err := store.Remember(ctx, "user-1", testEntry{Content: content}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := store.Recall(ctx, "user-1", RecallQuery{Text: "query"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("zero Limit must return all results: got %d, want 3", len(all))
	}

	filtered, err := store.Recall(ctx, "user-1", RecallQuery{Text: "query", MinSimilarity: 0.75})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Value.Content != "exact" {
		t.Fatalf("minimum similarity was not applied: %#v", filtered)
	}

	limited, err := store.Recall(ctx, "user-1", RecallQuery{Text: "query", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Fatalf("Limit = 2 returned %d entries", len(limited))
	}
}

func TestStore_RecallQueryErrors(t *testing.T) {
	store, err := NewStore[testEntry](newMockEmbedder(2))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	invalid := []RecallQuery{
		{},
		{Text: "query", Limit: -1},
		{Text: "query", MinSimilarity: -0.1},
		{Text: "query", MinSimilarity: 1.1},
	}
	for _, query := range invalid {
		if _, err := store.Recall(ctx, "user-1", query); !errors.Is(err, ErrInvalidRecallQuery) {
			t.Errorf("Recall(%+v) error = %v, want ErrInvalidRecallQuery", query, err)
		}
	}

	_, err = store.Recall(ctx, "user-1", RecallQuery{
		Text:    "query",
		Filters: []Filter{{Field: "score", Operator: FilterGreaterThan, Value: 0.5}},
	})
	if !errors.Is(err, ErrUnsupportedFilter) {
		t.Fatalf("filter error = %v, want ErrUnsupportedFilter", err)
	}

	_, err = store.Recall(ctx, "user-1", RecallQuery{
		Text:  "query",
		Order: []Order{{Field: "score", Direction: OrderDescending}},
	})
	if !errors.Is(err, ErrUnsupportedOrder) {
		t.Fatalf("order error = %v, want ErrUnsupportedOrder", err)
	}
}
