package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func setupMiniredis(t *testing.T) (*miniredis.Miniredis, *goredis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{
		Addr: mr.Addr(),
	})
	return mr, client
}

func TestIncrementRequests_CountsWithinWindow(t *testing.T) {
	mr, client := setupMiniredis(t)
	_ = mr
	store := NewStore(client)
	ctx := context.Background()
	window := 10 * time.Second

	// Increment multiple times and verify count increases
	for i := 1; i <= 5; i++ {
		count, err := store.IncrementRequests(ctx, "key1", window)
		if err != nil {
			t.Fatalf("IncrementRequests iteration %d: unexpected error: %v", i, err)
		}
		if count != i {
			t.Errorf("IncrementRequests iteration %d: got count %d, want %d", i, count, i)
		}
	}
}

func TestGetRequestCount_ReturnsCountWithinWindow(t *testing.T) {
	mr, client := setupMiniredis(t)
	_ = mr
	store := NewStore(client)
	ctx := context.Background()
	window := 10 * time.Second

	// Add several entries
	for i := 0; i < 3; i++ {
		_, err := store.IncrementRequests(ctx, "key1", window)
		if err != nil {
			t.Fatalf("IncrementRequests: unexpected error: %v", err)
		}
	}

	// GetRequestCount should return the same count
	count, err := store.GetRequestCount(ctx, "key1", window)
	if err != nil {
		t.Fatalf("GetRequestCount: unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("GetRequestCount: got %d, want 3", count)
	}
}

func TestIncrementTokens_SumsAmounts(t *testing.T) {
	mr, client := setupMiniredis(t)
	_ = mr
	store := NewStore(client)
	ctx := context.Background()
	window := 10 * time.Second

	amounts := []int{10, 25, 100, 50}
	expectedTotal := 0

	for i, amt := range amounts {
		expectedTotal += amt
		total, err := store.IncrementTokens(ctx, "key1", window, amt)
		if err != nil {
			t.Fatalf("IncrementTokens iteration %d: unexpected error: %v", i, err)
		}
		if total != expectedTotal {
			t.Errorf("IncrementTokens iteration %d: got total %d, want %d", i, total, expectedTotal)
		}
	}
}

func TestGetTokenCount_ReturnsTokenSumWithinWindow(t *testing.T) {
	mr, client := setupMiniredis(t)
	_ = mr
	store := NewStore(client)
	ctx := context.Background()
	window := 10 * time.Second

	// Add token entries with different amounts
	amounts := []int{15, 30, 55}
	for i, amt := range amounts {
		_, err := store.IncrementTokens(ctx, "key1", window, amt)
		if err != nil {
			t.Fatalf("IncrementTokens iteration %d: unexpected error: %v", i, err)
		}
	}

	// GetTokenCount should return the sum
	total, err := store.GetTokenCount(ctx, "key1", window)
	if err != nil {
		t.Fatalf("GetTokenCount: unexpected error: %v", err)
	}
	expectedTotal := 15 + 30 + 55
	if total != expectedTotal {
		t.Errorf("GetTokenCount: got %d, want %d", total, expectedTotal)
	}
}

func TestTTLExpiry(t *testing.T) {
	mr, client := setupMiniredis(t)
	store := NewStore(client)
	ctx := context.Background()
	window := 2 * time.Second

	// Add request entries
	_, err := store.IncrementRequests(ctx, "key1", window)
	if err != nil {
		t.Fatalf("IncrementRequests: unexpected error: %v", err)
	}

	// Add token entries
	_, err = store.IncrementTokens(ctx, "key1", window, 42)
	if err != nil {
		t.Fatalf("IncrementTokens: unexpected error: %v", err)
	}

	// Verify entries exist
	reqCount, err := store.GetRequestCount(ctx, "key1", window)
	if err != nil {
		t.Fatalf("GetRequestCount before expiry: unexpected error: %v", err)
	}
	if reqCount != 1 {
		t.Errorf("GetRequestCount before expiry: got %d, want 1", reqCount)
	}

	tokCount, err := store.GetTokenCount(ctx, "key1", window)
	if err != nil {
		t.Fatalf("GetTokenCount before expiry: unexpected error: %v", err)
	}
	if tokCount != 42 {
		t.Errorf("GetTokenCount before expiry: got %d, want 42", tokCount)
	}

	// Fast-forward time past the window using miniredis
	mr.FastForward(3 * time.Second)

	// After window passes, entries should be pruned on next query
	reqCount, err = store.GetRequestCount(ctx, "key1", window)
	if err != nil {
		t.Fatalf("GetRequestCount after expiry: unexpected error: %v", err)
	}
	if reqCount != 0 {
		t.Errorf("GetRequestCount after expiry: got %d, want 0", reqCount)
	}

	tokCount, err = store.GetTokenCount(ctx, "key1", window)
	if err != nil {
		t.Fatalf("GetTokenCount after expiry: unexpected error: %v", err)
	}
	if tokCount != 0 {
		t.Errorf("GetTokenCount after expiry: got %d, want 0", tokCount)
	}
}

func TestErrorPropagation(t *testing.T) {
	mr, _ := setupMiniredis(t)

	// Create a client with minimal retries and short timeouts so the test is fast
	client := goredis.NewClient(&goredis.Options{
		Addr:        mr.Addr(),
		MaxRetries:  0,
		DialTimeout: 100 * time.Millisecond,
		ReadTimeout: 100 * time.Millisecond,
		PoolSize:    1,
	})
	store := NewStore(client)
	ctx := context.Background()
	window := 10 * time.Second

	// Close the miniredis server to simulate Redis unavailability
	mr.Close()

	// All operations should return errors
	_, err := store.IncrementRequests(ctx, "key1", window)
	if err == nil {
		t.Error("IncrementRequests: expected error when Redis is unavailable, got nil")
	}

	_, err = store.IncrementTokens(ctx, "key1", window, 10)
	if err == nil {
		t.Error("IncrementTokens: expected error when Redis is unavailable, got nil")
	}

	_, err = store.GetRequestCount(ctx, "key1", window)
	if err == nil {
		t.Error("GetRequestCount: expected error when Redis is unavailable, got nil")
	}

	_, err = store.GetTokenCount(ctx, "key1", window)
	if err == nil {
		t.Error("GetTokenCount: expected error when Redis is unavailable, got nil")
	}
}

func TestWithPrefix_NamespacesKeys(t *testing.T) {
	mr, client := setupMiniredis(t)
	_ = mr
	store := NewStore(client, WithPrefix("myapp"))
	ctx := context.Background()
	window := 10 * time.Second

	_, err := store.IncrementRequests(ctx, "user1", window)
	if err != nil {
		t.Fatalf("IncrementRequests: unexpected error: %v", err)
	}

	// Verify the key is namespaced correctly
	keys := mr.Keys()
	found := false
	for _, k := range keys {
		if k == "myapp:req:user1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected key 'myapp:req:user1' in Redis, got keys: %v", keys)
	}
}

func TestSeparateKeys_IndependentCounts(t *testing.T) {
	mr, client := setupMiniredis(t)
	_ = mr
	store := NewStore(client)
	ctx := context.Background()
	window := 10 * time.Second

	// Increment different keys
	for i := 0; i < 3; i++ {
		_, _ = store.IncrementRequests(ctx, "keyA", window)
	}
	for i := 0; i < 5; i++ {
		_, _ = store.IncrementRequests(ctx, "keyB", window)
	}

	countA, err := store.GetRequestCount(ctx, "keyA", window)
	if err != nil {
		t.Fatalf("GetRequestCount keyA: unexpected error: %v", err)
	}
	if countA != 3 {
		t.Errorf("GetRequestCount keyA: got %d, want 3", countA)
	}

	countB, err := store.GetRequestCount(ctx, "keyB", window)
	if err != nil {
		t.Fatalf("GetRequestCount keyB: unexpected error: %v", err)
	}
	if countB != 5 {
		t.Errorf("GetRequestCount keyB: got %d, want 5", countB)
	}
}
