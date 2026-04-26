package redis

import (
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/memory"
)

// ---------------------------------------------------------------------------
// Unit Tests
// ---------------------------------------------------------------------------

// TestNew_NilEmbedder verifies that New returns an error when embedder is nil.
func TestNew_NilEmbedder(t *testing.T) {
	_, err := New(Options{}, nil, 128)
	if err == nil {
		t.Fatal("expected error for nil embedder, got nil")
	}
	if !strings.Contains(err.Error(), "embedder is required") {
		t.Fatalf("expected error containing 'embedder is required', got: %v", err)
	}
}

// TestNew_InvalidDim verifies that New returns an error when dim < 1.
func TestNew_InvalidDim(t *testing.T) {
	emb := &hashEmbedder{dim: 8}

	// dim = 0
	_, err := New(Options{}, emb, 0)
	if err == nil {
		t.Fatal("expected error for dim=0, got nil")
	}
	if !strings.Contains(err.Error(), "dim must be at least 1") {
		t.Fatalf("expected error containing 'dim must be at least 1', got: %v", err)
	}

	// dim = -1
	_, err = New(Options{}, emb, -1)
	if err == nil {
		t.Fatal("expected error for dim=-1, got nil")
	}
	if !strings.Contains(err.Error(), "dim must be at least 1") {
		t.Fatalf("expected error containing 'dim must be at least 1', got: %v", err)
	}
}

// TestRemember_EmptyIdentifier verifies that Remember returns an error for
// an empty identifier. Does not require Redis.
func TestRemember_EmptyIdentifier(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	err := store.Remember(t.Context(), "", "some fact", nil)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	if !strings.Contains(err.Error(), "identifier must not be empty") {
		t.Fatalf("expected error containing 'identifier must not be empty', got: %v", err)
	}
}

// TestRemember_EmptyFact verifies that Remember returns an error for
// an empty fact. Does not require Redis.
func TestRemember_EmptyFact(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	err := store.Remember(t.Context(), "user-1", "", nil)
	if err == nil {
		t.Fatal("expected error for empty fact, got nil")
	}
	if !strings.Contains(err.Error(), "fact must not be empty") {
		t.Fatalf("expected error containing 'fact must not be empty', got: %v", err)
	}
}

// TestRecall_EmptyIdentifier verifies that Recall returns an error for
// an empty identifier. Does not require Redis.
func TestRecall_EmptyIdentifier(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	_, err := store.Recall(t.Context(), "", "query", 5)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	if !strings.Contains(err.Error(), "identifier must not be empty") {
		t.Fatalf("expected error containing 'identifier must not be empty', got: %v", err)
	}
}

// TestRecall_InvalidLimit verifies that Recall returns an error for limit < 1.
// Does not require Redis.
func TestRecall_InvalidLimit(t *testing.T) {
	addr := skipIfNoRedis(t)
	store := newTestStore(t, addr)

	// limit = 0
	_, err := store.Recall(t.Context(), "user-1", "query", 0)
	if err == nil {
		t.Fatal("expected error for limit=0, got nil")
	}
	if !strings.Contains(err.Error(), "limit must be at least 1") {
		t.Fatalf("expected error containing 'limit must be at least 1', got: %v", err)
	}

	// limit = -1
	_, err = store.Recall(t.Context(), "user-1", "query", -1)
	if err == nil {
		t.Fatal("expected error for limit=-1, got nil")
	}
	if !strings.Contains(err.Error(), "limit must be at least 1") {
		t.Fatalf("expected error containing 'limit must be at least 1', got: %v", err)
	}
}

// TestInterfaceAssertion verifies at runtime that *Store satisfies the
// memory.Memory interface.
func TestInterfaceAssertion(t *testing.T) {
	// Compile-time check is already in redis.go; this is a runtime assertion.
	var _ memory.Memory = (*Store)(nil)
}
