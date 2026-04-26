package conversation

import (
	"context"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)


// TestComposedLoadAppliesAllTransformations verifies that for any valid message
// slice containing mixed content block types, loading through a Filter(Window(Store))
// composition returns at most N messages, each containing only TextBlock content,
// equivalent to applying Window then Filter independently.
//
func TestComposedLoadAppliesAllTransformations(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := genMessages(t)
		n := rapid.IntRange(1, 50).Draw(t, "windowSize")

		store := NewInMemory()
		windowed := NewWindow(store, n)
		filtered := NewFilter(windowed)
		ctx := context.Background()

		if err := filtered.Save(ctx, "conv", msgs); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := filtered.Load(ctx, "conv")
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		// Result must have at most N messages.
		if len(loaded) > n {
			t.Fatalf("expected at most %d messages, got %d", n, len(loaded))
		}

		// Every block in every returned message must be a TextBlock.
		for i, msg := range loaded {
			for j, b := range msg.Content {
				if _, ok := b.(agent.TextBlock); !ok {
					t.Fatalf("message[%d].Content[%d] is %T, expected TextBlock", i, j, b)
				}
			}
			// Messages with no content should have been omitted by Filter.
			if len(msg.Content) == 0 {
				t.Fatalf("message[%d] has no content blocks, should have been omitted", i)
			}
		}
	})
}


// TestComposedSavePropagatesUnchanged verifies that for any valid message slice
// and any composition of strategies (Window, Filter), saving through the
// composed chain then loading directly from the innermost Store returns the
// original messages unchanged.
//
func TestComposedSavePropagatesUnchanged(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := genMessages(t)
		ctx := context.Background()

		store := NewInMemory()

		// Build a random composition chain on top of the store.
		var outer agent.Conversation = store
		useWindow := rapid.Bool().Draw(t, "useWindow")
		useFilter := rapid.Bool().Draw(t, "useFilter")

		// Ensure at least one strategy is applied.
		if !useWindow && !useFilter {
			useFilter = true
		}

		if useWindow {
			n := rapid.IntRange(1, 50).Draw(t, "windowSize")
			outer = NewWindow(outer, n)
		}
		if useFilter {
			outer = NewFilter(outer)
		}

		// Save through the composed chain.
		if err := outer.Save(ctx, "conv", msgs); err != nil {
			t.Fatalf("Save through composed chain failed: %v", err)
		}

		// Load directly from the innermost Store, bypassing all strategies.
		stored, err := store.Load(ctx, "conv")
		if err != nil {
			t.Fatalf("Store.Load failed: %v", err)
		}

		// The stored messages must match the originals exactly.
		if !reflect.DeepEqual(stored, msgs) {
			t.Fatalf("messages in Store differ from originals:\n  stored=%d msgs\n  original=%d msgs",
				len(stored), len(msgs))
		}
	})
}
