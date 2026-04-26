package memory

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestRemember_EmptyFact verifies that Remember returns an error when the fact
// string is empty.
//
// Validates: Requirement 1.4
func TestRemember_EmptyFact(t *testing.T) {
	store := NewStore(&hashEmbedder{dim: 8})
	err := store.Remember(context.Background(), "user-1", "", nil)
	if err == nil {
		t.Fatal("expected error for empty fact, got nil")
	}
	const want = "memory: fact must not be empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestRemember_EmptyUserID verifies that Remember returns an error when the
// identifier is empty.
//
// Validates: Requirement 1.5
func TestRemember_EmptyUserID(t *testing.T) {
	store := NewStore(&hashEmbedder{dim: 8})
	err := store.Remember(context.Background(), "", "some fact", nil)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	const want = "memory: identifier must not be empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestRecall_EmptyUserID verifies that Recall returns an error when the
// identifier is empty.
//
// Validates: Requirement 1.7
func TestRecall_EmptyUserID(t *testing.T) {
	store := NewStore(&hashEmbedder{dim: 8})
	results, err := store.Recall(context.Background(), "", "query", 5)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
	const want = "memory: identifier must not be empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	if results != nil {
		t.Fatalf("expected nil results on error, got %v", results)
	}
}

// TestRecall_InvalidLimit verifies that Recall returns an error when the limit
// is less than 1.
//
// Validates: Requirement 1.6
func TestRecall_InvalidLimit(t *testing.T) {
	store := NewStore(&hashEmbedder{dim: 8})

	for _, limit := range []int{0, -1, -100} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			results, err := store.Recall(context.Background(), "user-1", "query", limit)
			if err == nil {
				t.Fatalf("expected error for limit %d, got nil", limit)
			}
			const want = "memory: limit must be at least 1"
			if err.Error() != want {
				t.Fatalf("error = %q, want %q", err.Error(), want)
			}
			if results != nil {
				t.Fatalf("expected nil results on error, got %v", results)
			}
		})
	}
}

// TestRecall_NoEntries verifies that Recall for a user with no stored entries
// returns an empty non-nil slice and no error.
//
// Validates: Requirement 1.8
func TestRecall_NoEntries(t *testing.T) {
	store := NewStore(&hashEmbedder{dim: 8})
	results, err := store.Recall(context.Background(), "unknown-user", "query", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(results) != 0 {
		t.Fatalf("expected empty slice, got %d elements", len(results))
	}
}

// TestStore_ConcurrentAccess verifies that the Store is safe for concurrent use
// by multiple goroutines. It launches 10 goroutines each performing 100
// Remember+Recall operations. Run with -race to detect data races.
//
// Validates: Requirement 2.6
func TestStore_ConcurrentAccess(t *testing.T) {
	store := NewStore(&hashEmbedder{dim: 8})
	ctx := context.Background()

	const goroutines = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(id int) {
			defer wg.Done()
			userID := fmt.Sprintf("user-%d", id)
			for i := range opsPerGoroutine {
				fact := fmt.Sprintf("fact-%d-%d", id, i)
				if err := store.Remember(ctx, userID, fact, nil); err != nil {
					t.Errorf("goroutine %d: Remember failed: %v", id, err)
					return
				}
				results, err := store.Recall(ctx, userID, fact, 5)
				if err != nil {
					t.Errorf("goroutine %d: Recall failed: %v", id, err)
					return
				}
				if results == nil {
					t.Errorf("goroutine %d: Recall returned nil slice", id)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

// TestRememberTool_NoUserID verifies that RememberTool returns an error when
// no identifier is present on the context.
//
// Validates: Requirement 5.3
func TestRememberTool_NoUserID(t *testing.T) {
	spy := &spyMemory{}
	rt := RememberTool(spy)

	// Use a bare context with no identifier attached.
	ctx := context.Background()
	input := []byte(`{"fact":"some fact"}`)

	_, err := rt.Handler(ctx, input)
	if err == nil {
		t.Fatal("expected error when identifier is missing, got nil")
	}
	const want = "memory: identifier not found in context; use agent.WithIdentifier"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestRecallTool_NoUserID verifies that RecallTool returns an error when
// no identifier is present on the context.
//
// Validates: Requirement 5.3
func TestRecallTool_NoUserID(t *testing.T) {
	spy := &spyMemory{}
	ct := RecallTool(spy)

	// Use a bare context with no identifier attached.
	ctx := context.Background()
	input := []byte(`{"query":"anything"}`)

	_, err := ct.Handler(ctx, input)
	if err == nil {
		t.Fatal("expected error when identifier is missing, got nil")
	}
	const want = "memory: identifier not found in context; use agent.WithIdentifier"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestRecallTool_NoResults verifies that RecallTool returns "No relevant
// memories found." when Recall returns an empty slice.
//
// Validates: Requirement 4.5
func TestRecallTool_NoResults(t *testing.T) {
	spy := &spyMemory{recallResult: []Entry{}}
	ct := RecallTool(spy)

	ctx := agent.WithIdentifier(context.Background(), "user-1")
	input := []byte(`{"query":"anything"}`)

	result, err := ct.Handler(ctx, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "No relevant memories found."
	if result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
}

// TestRememberTool_Name verifies that RememberTool has the name "remember".
//
// Validates: Requirement 3.6
func TestRememberTool_Name(t *testing.T) {
	spy := &spyMemory{}
	rt := RememberTool(spy)

	const want = "remember"
	if rt.Spec.Name != want {
		t.Fatalf("RememberTool name = %q, want %q", rt.Spec.Name, want)
	}
}

// TestRecallTool_Name verifies that RecallTool has the name "recall".
//
// Validates: Requirement 4.7
func TestRecallTool_Name(t *testing.T) {
	spy := &spyMemory{}
	ct := RecallTool(spy)

	const want = "recall"
	if ct.Spec.Name != want {
		t.Fatalf("RecallTool name = %q, want %q", ct.Spec.Name, want)
	}
}
