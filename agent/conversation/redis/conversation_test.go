package redis

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

// skipIfNoRedis skips the test if REDIS_ADDR is not set and returns the address.
func skipIfNoRedis(t *testing.T) string {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set, skipping integration test")
	}
	return addr
}

func genMessages(t *rapid.T) []agent.Message { return testutil.GenMessages(t, 10) }

// Feature: redis-providers, Property 1: Memory Save/Load Round-Trip
// **Validates: Requirements 3.1, 3.5, 4.1, 5.1, 5.2, 5.3**
func TestProperty_MemorySaveLoadRoundTrip(t *testing.T) {
	addr := skipIfNoRedis(t)

	mem, err := New(RedisOptions{Addr: addr})
	if err != nil {
		t.Fatalf("failed to create RedisConversation: %v", err)
	}
	defer mem.Close()

	rapid.Check(t, func(t *rapid.T) {
		messages := genMessages(t)
		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")

		ctx := context.Background()

		if err := mem.Save(ctx, convID, messages); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		// Clean up the key after the test iteration.
		defer mem.client.Del(ctx, mem.keyPrefix+convID)

		loaded, err := mem.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if !reflect.DeepEqual(messages, loaded) {
			t.Fatalf("round-trip mismatch:\n  saved:  %+v\n  loaded: %+v", messages, loaded)
		}
	})
}

// --- Unit Tests for RedisConversation (Task 2.5) ---

// TestNew_UnreachableAddr verifies that NewRedisConversation returns an error
// containing "ping" when the Redis address is unreachable.
// Validates: Requirement 2.3
func TestNew_UnreachableAddr(t *testing.T) {
	_, err := New(RedisOptions{Addr: "localhost:1"})
	if err == nil {
		t.Fatal("expected error for unreachable address, got nil")
	}
	if !contains(err.Error(), "ping") {
		t.Fatalf("expected error to contain 'ping', got: %v", err)
	}
}

// TestRedisConversation_LoadNonExistent verifies that Load for a non-existent conversation ID
// returns an empty (non-nil) slice and nil error.
// Validates: Requirement 4.2
func TestRedisConversation_LoadNonExistent(t *testing.T) {
	addr := skipIfNoRedis(t)

	mem, err := New(RedisOptions{Addr: addr})
	if err != nil {
		t.Fatalf("failed to create RedisConversation: %v", err)
	}
	defer mem.Close()

	msgs, err := mem.Load(context.Background(), "nonexistent-conv-id-12345")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if msgs == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(msgs) != 0 {
		t.Fatalf("expected empty slice, got %d messages", len(msgs))
	}
}

// TestRedisConversation_DefaultKeyPrefix verifies that a newly created RedisConversation
// has the default key prefix "gude:".
// Validates: Requirement 2.7
func TestRedisConversation_DefaultKeyPrefix(t *testing.T) {
	addr := skipIfNoRedis(t)

	mem, err := New(RedisOptions{Addr: addr})
	if err != nil {
		t.Fatalf("failed to create RedisConversation: %v", err)
	}
	defer mem.Close()

	if mem.keyPrefix != "gude:" {
		t.Fatalf("expected default keyPrefix %q, got %q", "gude:", mem.keyPrefix)
	}
}

// TestRedisConversation_TTLSet verifies that when WithTTL is configured, saved keys
// have a TTL set in Redis.
// Validates: Requirements 2.5, 3.2
func TestRedisConversation_TTLSet(t *testing.T) {
	addr := skipIfNoRedis(t)

	ttl := 10 * time.Minute
	mem, err := New(RedisOptions{Addr: addr}, WithTTL(ttl))
	if err != nil {
		t.Fatalf("failed to create RedisConversation: %v", err)
	}
	defer mem.Close()

	ctx := context.Background()
	convID := "test-ttl-set-conv"
	key := mem.keyPrefix + convID

	// Clean up before and after.
	mem.client.Del(ctx, key)
	defer mem.client.Del(ctx, key)

	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
	}
	if err := mem.Save(ctx, convID, msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	remaining := mem.client.TTL(ctx, key).Val()
	if remaining <= 0 {
		t.Fatalf("expected positive TTL, got %v", remaining)
	}
	if remaining > ttl {
		t.Fatalf("TTL %v exceeds configured %v", remaining, ttl)
	}
}

// TestRedisConversation_NoExpiration verifies that when no TTL is configured (default),
// saved keys have no expiration (TTL returns -1 in Redis).
// Validates: Requirements 2.6, 3.3
func TestRedisConversation_NoExpiration(t *testing.T) {
	addr := skipIfNoRedis(t)

	mem, err := New(RedisOptions{Addr: addr})
	if err != nil {
		t.Fatalf("failed to create RedisConversation: %v", err)
	}
	defer mem.Close()

	ctx := context.Background()
	convID := "test-no-expiration-conv"
	key := mem.keyPrefix + convID

	// Clean up before and after.
	mem.client.Del(ctx, key)
	defer mem.client.Del(ctx, key)

	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
	}
	if err := mem.Save(ctx, convID, msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	remaining := mem.client.TTL(ctx, key).Val()
	// Redis returns -1 for keys with no expiration.
	if remaining != -1*time.Second {
		t.Fatalf("expected TTL of -1 (no expiration), got %v", remaining)
	}
}

// contains checks if s contains substr (helper to avoid importing strings).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstr(s, substr)
}

func searchSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestRedisConversation_WithOption verifies that RedisConversation is accepted by agent.WithMemory.
// Validates: Requirement 2.4
func TestRedisConversation_WithOption(t *testing.T) {
	addr := skipIfNoRedis(t)
	mem, err := New(RedisOptions{Addr: addr})
	if err != nil {
		t.Fatalf("failed to create RedisConversation: %v", err)
	}
	defer mem.Close()

	// This should compile and not panic — proves RedisConversation satisfies the Memory interface
	// used by WithMemory.
	opt := agent.WithMemory(mem, "test-conv")
	if opt == nil {
		t.Fatal("expected non-nil option from WithMemory")
	}
}

// --- Integration Tests for RedisConversation List and Delete (Task 8.4) ---

// TestRedisConversation_ListReturnsSavedConversationIDs verifies that List returns
// all conversation IDs that have been saved, using a unique key prefix.
// Validates: Requirement 5.5
func TestRedisConversation_ListReturnsSavedConversationIDs(t *testing.T) {
	addr := skipIfNoRedis(t)

	prefix := "test-list-delete:"
	mem, err := New(RedisOptions{Addr: addr}, WithKeyPrefix(prefix))
	if err != nil {
		t.Fatalf("failed to create RedisConversation: %v", err)
	}
	defer mem.Close()

	ctx := context.Background()

	convIDs := []string{"conv-alpha", "conv-beta", "conv-gamma"}
	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
	}

	// Save 3 conversations.
	for _, id := range convIDs {
		if err := mem.Save(ctx, id, msgs); err != nil {
			t.Fatalf("Save(%q) failed: %v", id, err)
		}
	}

	// Clean up after test.
	defer func() {
		for _, id := range convIDs {
			mem.client.Del(ctx, prefix+id)
		}
	}()

	// List and verify all 3 IDs are returned.
	listed, err := mem.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(listed) != len(convIDs) {
		t.Fatalf("expected %d IDs, got %d: %v", len(convIDs), len(listed), listed)
	}

	// Sort both slices for comparison since order is not guaranteed.
	sortStrings(listed)
	expected := make([]string, len(convIDs))
	copy(expected, convIDs)
	sortStrings(expected)

	if !reflect.DeepEqual(expected, listed) {
		t.Fatalf("List mismatch:\n  expected: %v\n  got:      %v", expected, listed)
	}
}

// TestRedisConversation_DeleteRemovesTargetKey verifies that Delete removes the target
// conversation while leaving other conversations intact.
// Validates: Requirement 6.5
func TestRedisConversation_DeleteRemovesTargetKey(t *testing.T) {
	addr := skipIfNoRedis(t)

	prefix := "test-del-target:"
	mem, err := New(RedisOptions{Addr: addr}, WithKeyPrefix(prefix))
	if err != nil {
		t.Fatalf("failed to create RedisConversation: %v", err)
	}
	defer mem.Close()

	ctx := context.Background()

	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
	}

	// Save 2 conversations.
	if err := mem.Save(ctx, "keep-me", msgs); err != nil {
		t.Fatalf("Save(keep-me) failed: %v", err)
	}
	if err := mem.Save(ctx, "delete-me", msgs); err != nil {
		t.Fatalf("Save(delete-me) failed: %v", err)
	}

	// Clean up after test.
	defer func() {
		mem.client.Del(ctx, prefix+"keep-me")
		mem.client.Del(ctx, prefix+"delete-me")
	}()

	// Delete one conversation.
	if err := mem.Delete(ctx, "delete-me"); err != nil {
		t.Fatalf("Delete(delete-me) failed: %v", err)
	}

	// Verify deleted conversation is gone from List.
	listed, err := mem.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	for _, id := range listed {
		if id == "delete-me" {
			t.Fatal("deleted conversation still appears in List")
		}
	}

	// Verify Load returns empty for deleted conversation.
	loaded, err := mem.Load(ctx, "delete-me")
	if err != nil {
		t.Fatalf("Load(delete-me) failed: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected empty messages for deleted conversation, got %d", len(loaded))
	}

	// Verify the other conversation still exists.
	remaining, err := mem.Load(ctx, "keep-me")
	if err != nil {
		t.Fatalf("Load(keep-me) failed: %v", err)
	}
	if !reflect.DeepEqual(msgs, remaining) {
		t.Fatalf("remaining conversation mismatch:\n  expected: %+v\n  got:      %+v", msgs, remaining)
	}
}

// sortStrings sorts a string slice in place (simple insertion sort to avoid importing sort).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
