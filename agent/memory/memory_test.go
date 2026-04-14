package memory

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

func TestStore_LoadSaveRoundTrip(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi there"}}},
	}

	if err := store.Save(ctx, "conv-1", msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(loaded))
	}

	for i, m := range loaded {
		if m.Role != msgs[i].Role {
			t.Errorf("message[%d] role: expected %q, got %q", i, msgs[i].Role, m.Role)
		}
		tb := m.Content[0].(agent.TextBlock)
		origTB := msgs[i].Content[0].(agent.TextBlock)
		if tb.Text != origTB.Text {
			t.Errorf("message[%d] text: expected %q, got %q", i, origTB.Text, tb.Text)
		}
	}
}

func TestStore_LoadReturnsEmptyForUnknownID(t *testing.T) {
	store := NewStore()
	loaded, err := store.Load(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 messages, got %d", len(loaded))
	}
}

func TestStore_SaveReturnsCopy(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "original"}}},
	}

	if err := store.Save(ctx, "conv-1", msgs); err != nil {
		t.Fatal(err)
	}

	msgs[0] = agent.Message{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "mutated"}}}

	loaded, err := store.Load(ctx, "conv-1")
	if err != nil {
		t.Fatal(err)
	}

	tb := loaded[0].Content[0].(agent.TextBlock)
	if tb.Text != "original" {
		t.Errorf("expected %q, got %q (mutation leaked)", "original", tb.Text)
	}
}

// Feature: memory-strategies, Property 5: List returns all saved conversation IDs

// TestStoreListReturnsAllSavedConversationIDs verifies that for any set of
// distinct conversation IDs saved to a Store, calling List returns a slice
// containing exactly those IDs (in any order), with no duplicates and no
// missing entries.
//
// **Validates: Requirements 5.3, 5.4**
func TestStoreListReturnsAllSavedConversationIDs(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numConvs := rapid.IntRange(1, 20).Draw(t, "numConversations")

		// Generate distinct conversation IDs.
		idSet := make(map[string]bool, numConvs)
		for len(idSet) < numConvs {
			id := rapid.StringMatching(`[a-zA-Z0-9_-]{1,30}`).Draw(t, fmt.Sprintf("convID_%d", len(idSet)))
			idSet[id] = true
		}

		store := NewStore()
		ctx := context.Background()

		// Save a message for each conversation ID.
		for id := range idSet {
			msgs := []agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello from " + id}}},
			}
			if err := store.Save(ctx, id, msgs); err != nil {
				t.Fatalf("Save failed for %q: %v", id, err)
			}
		}

		listed, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}

		// Assert exact match: same length, same elements.
		if len(listed) != numConvs {
			t.Fatalf("expected %d conversation IDs, got %d", numConvs, len(listed))
		}

		sort.Strings(listed)
		expected := make([]string, 0, numConvs)
		for id := range idSet {
			expected = append(expected, id)
		}
		sort.Strings(expected)

		if !reflect.DeepEqual(listed, expected) {
			t.Fatalf("List mismatch:\n  got:      %v\n  expected: %v", listed, expected)
		}
	})
}

// Feature: memory-strategies, Property 6: Delete removes exactly the target conversation

// TestStoreDeleteRemovesExactlyTargetConversation verifies that for any Store
// containing a set of conversations, deleting one conversation by ID causes
// that ID to no longer appear in List results and its messages to no longer be
// loadable, while all other conversations remain unchanged.
//
// **Validates: Requirements 6.2, 6.3**
func TestStoreDeleteRemovesExactlyTargetConversation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numConvs := rapid.IntRange(2, 10).Draw(t, "numConversations")

		// Generate distinct conversation IDs.
		ids := make([]string, 0, numConvs)
		idSet := make(map[string]bool, numConvs)
		for len(idSet) < numConvs {
			id := rapid.StringMatching(`[a-zA-Z0-9_-]{1,30}`).Draw(t, fmt.Sprintf("convID_%d", len(idSet)))
			if !idSet[id] {
				idSet[id] = true
				ids = append(ids, id)
			}
		}

		store := NewStore()
		ctx := context.Background()

		// Save messages for each conversation, keeping a copy for later comparison.
		savedMsgs := make(map[string][]agent.Message, numConvs)
		for _, id := range ids {
			msgs := []agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "msg for " + id}}},
			}
			savedMsgs[id] = msgs
			if err := store.Save(ctx, id, msgs); err != nil {
				t.Fatalf("Save failed for %q: %v", id, err)
			}
		}

		// Pick a random target to delete.
		targetIdx := rapid.IntRange(0, numConvs-1).Draw(t, "targetIndex")
		targetID := ids[targetIdx]

		if err := store.Delete(ctx, targetID); err != nil {
			t.Fatalf("Delete failed for %q: %v", targetID, err)
		}

		// Assert target is gone from List.
		listed, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}

		listedSet := make(map[string]bool, len(listed))
		for _, id := range listed {
			listedSet[id] = true
		}

		if listedSet[targetID] {
			t.Fatalf("deleted conversation %q still appears in List", targetID)
		}

		// Assert target Load returns empty.
		loaded, err := store.Load(ctx, targetID)
		if err != nil {
			t.Fatalf("Load failed for deleted %q: %v", targetID, err)
		}
		if len(loaded) != 0 {
			t.Fatalf("expected 0 messages for deleted %q, got %d", targetID, len(loaded))
		}

		// Assert all other conversations are unchanged.
		for _, id := range ids {
			if id == targetID {
				continue
			}
			if !listedSet[id] {
				t.Fatalf("non-deleted conversation %q missing from List", id)
			}
			loaded, err := store.Load(ctx, id)
			if err != nil {
				t.Fatalf("Load failed for %q: %v", id, err)
			}
			if !reflect.DeepEqual(loaded, savedMsgs[id]) {
				t.Fatalf("messages changed for non-deleted conversation %q", id)
			}
		}
	})
}
