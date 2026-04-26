package s3

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

func genMessages(t *rapid.T) []agent.Message { return testutil.GenMessages(t, 10) }

// Feature: memory-drivers, Property 2: Blob Save/Load round-trip
// **Validates: Requirements 4.1, 5.1, 5.4**
func TestProperty_BlobSaveLoadRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockS3Client()
		m := &S3Conversation{
			client:    mock,
			bucket:    "test-bucket",
			keyPrefix: "",
		}

		messages := genMessages(t)
		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")

		ctx := context.Background()

		if err := m.Save(ctx, convID, messages); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := m.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if !reflect.DeepEqual(messages, loaded) {
			t.Fatalf("round-trip mismatch:\n  saved:  %+v\n  loaded: %+v", messages, loaded)
		}
	})
}

// Feature: memory-drivers, Property 3: Blob key formation
// **Validates: Requirements 2.2, 2.3**
func TestProperty_BlobKeyFormation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockS3Client()

		prefix := rapid.StringMatching(`[a-zA-Z0-9:_-]{1,20}`).Draw(t, "prefix")
		convID := rapid.StringMatching(`[a-zA-Z0-9]{4,16}`).Draw(t, "convID")

		m := &S3Conversation{
			client:    mock,
			bucket:    "test-bucket",
			keyPrefix: prefix,
		}

		ctx := context.Background()

		messages := []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		}

		if err := m.Save(ctx, convID, messages); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		expectedKey := prefix + convID
		if _, ok := mock.objects[expectedKey]; !ok {
			t.Fatalf("expected object at key %q, but found keys: %v", expectedKey, keysOf(mock.objects))
		}
	})
}

// Feature: memory-drivers, Property 4: Blob overwrite
// **Validates: Requirement 4.4**
func TestProperty_BlobOverwrite(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockS3Client()
		m := &S3Conversation{
			client:    mock,
			bucket:    "test-bucket",
			keyPrefix: "",
		}

		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")
		messagesA := genMessages(t)
		messagesB := genMessages(t)

		ctx := context.Background()

		if err := m.Save(ctx, convID, messagesA); err != nil {
			t.Fatalf("Save(A) failed: %v", err)
		}
		if err := m.Save(ctx, convID, messagesB); err != nil {
			t.Fatalf("Save(B) failed: %v", err)
		}

		loaded, err := m.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if !reflect.DeepEqual(messagesB, loaded) {
			t.Fatalf("overwrite mismatch:\n  expected B: %+v\n  got:        %+v", messagesB, loaded)
		}
	})
}

// Feature: memory-drivers, Property 5: Blob List completeness
// **Validates: Requirement 6.1**
func TestProperty_BlobListCompleteness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockS3Client()

		// Use a unique isolated prefix per iteration to avoid cross-contamination.
		prefix := rapid.StringMatching(`pbt-[a-zA-Z0-9]{6,12}:`).Draw(t, "prefix")

		m := &S3Conversation{
			client:    mock,
			bucket:    "test-bucket",
			keyPrefix: prefix,
		}

		// Generate 1–5 distinct conversation IDs.
		n := rapid.IntRange(1, 5).Draw(t, "numConvs")
		ids := make([]string, n)
		seen := make(map[string]bool)
		for i := 0; i < n; i++ {
			var id string
			for {
				id = rapid.StringMatching(`[a-zA-Z0-9]{4,12}`).Draw(t, fmt.Sprintf("convID_%d", i))
				if !seen[id] {
					break
				}
			}
			seen[id] = true
			ids[i] = id
		}

		ctx := context.Background()
		msgs := []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		}

		for _, id := range ids {
			if err := m.Save(ctx, id, msgs); err != nil {
				t.Fatalf("Save(%q) failed: %v", id, err)
			}
		}

		listed, err := m.List(ctx)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}

		if len(listed) != len(ids) {
			t.Fatalf("List returned %d IDs, expected %d: listed=%v, saved=%v", len(listed), len(ids), listed, ids)
		}

		sort.Strings(listed)
		sort.Strings(ids)

		if !reflect.DeepEqual(ids, listed) {
			t.Fatalf("List mismatch:\n  expected: %v\n  got:      %v", ids, listed)
		}
	})
}

// Feature: memory-drivers, Property 6: Blob Delete then Load returns empty
// **Validates: Requirement 6.2**
func TestProperty_BlobDeleteThenLoad(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockS3Client()
		m := &S3Conversation{
			client:    mock,
			bucket:    "test-bucket",
			keyPrefix: "",
		}

		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")
		messages := genMessages(t)

		ctx := context.Background()

		if err := m.Save(ctx, convID, messages); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		if err := m.Delete(ctx, convID); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		loaded, err := m.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load after Delete failed: %v", err)
		}

		if loaded == nil {
			t.Fatal("expected non-nil empty slice after Delete, got nil")
		}
		if len(loaded) != 0 {
			t.Fatalf("expected empty slice after Delete, got %d messages", len(loaded))
		}
	})
}

// keysOf returns the keys of a map as a slice (for diagnostic messages).
func keysOf(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
