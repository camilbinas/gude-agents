package conversation

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)


// TestFilterRoundTripRetainsOnlyTextBlocks verifies that for any valid message
// slice, saving then loading through Filter returns messages containing only
// TextBlock content blocks. Every TextBlock from the original messages appears
// in the result, and messages that contained no TextBlock are omitted entirely.
//
func TestFilterRoundTripRetainsOnlyTextBlocks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := genMessages(t)

		store := NewInMemory()
		filter := NewFilter(store)
		ctx := context.Background()

		if err := filter.Save(ctx, "conv", msgs); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := filter.Load(ctx, "conv")
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		// 1. All returned blocks must be TextBlock
		for i, msg := range loaded {
			for j, b := range msg.Content {
				if _, ok := b.(agent.TextBlock); !ok {
					t.Fatalf("loaded[%d].Content[%d] is %T, expected TextBlock", i, j, b)
				}
			}
		}

		// 2. Collect all original TextBlocks and verify they all appear in result
		var expectedTextBlocks []agent.TextBlock
		for _, msg := range msgs {
			for _, b := range msg.Content {
				if tb, ok := b.(agent.TextBlock); ok {
					expectedTextBlocks = append(expectedTextBlocks, tb)
				}
			}
		}

		var gotTextBlocks []agent.TextBlock
		for _, msg := range loaded {
			for _, b := range msg.Content {
				gotTextBlocks = append(gotTextBlocks, b.(agent.TextBlock))
			}
		}

		if len(gotTextBlocks) != len(expectedTextBlocks) {
			t.Fatalf("expected %d TextBlocks, got %d", len(expectedTextBlocks), len(gotTextBlocks))
		}

		for i := range expectedTextBlocks {
			if gotTextBlocks[i].Text != expectedTextBlocks[i].Text {
				t.Fatalf("TextBlock[%d]: expected %q, got %q",
					i, expectedTextBlocks[i].Text, gotTextBlocks[i].Text)
			}
		}

		// 3. Messages that had no TextBlock in original should not appear in result
		expectedMsgCount := 0
		for _, msg := range msgs {
			hasText := false
			for _, b := range msg.Content {
				if _, ok := b.(agent.TextBlock); ok {
					hasText = true
					break
				}
			}
			if hasText {
				expectedMsgCount++
			}
		}

		if len(loaded) != expectedMsgCount {
			t.Fatalf("expected %d messages with TextBlocks, got %d", expectedMsgCount, len(loaded))
		}
	})
}
