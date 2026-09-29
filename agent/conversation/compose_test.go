package conversation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// TestComposedLoadAppliesAllTransformations verifies that for any valid message
// slice containing mixed content block types, loading through a Filter(Window(Store))
// composition returns at most N messages, each containing only TextBlock content,
// equivalent to applying Window then Filter independently.
func TestComposedLoadAppliesAllTransformations(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := genMessages(t)
		n := rapid.IntRange(1, 50).Draw(t, "windowSize")

		store := NewInMemory()
		windowed := NewWindow(store, n)
		filtered := NewFilter(windowed)
		ctx := context.Background()

		if err := saveLatest(ctx, filtered, "conv", msgs); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := loadMessages(ctx, filtered, "conv")
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
func TestComposedSavePropagatesUnchanged(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := genMessages(t)
		ctx := context.Background()

		store := NewInMemory()

		// Build a random composition chain on top of the store.
		var outer agent.ConversationStore = store
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
		if err := saveLatest(ctx, outer, "conv", msgs); err != nil {
			t.Fatalf("Save through composed chain failed: %v", err)
		}

		// Load directly from the innermost Store, bypassing all strategies.
		stored, err := loadMessages(ctx, store, "conv")
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

type flushingManager struct {
	*InMemory
	flushes int
}

func (m *flushingManager) Flush(context.Context) error {
	m.flushes++
	return nil
}

func TestComposedStrategiesPropagateRevisionConflictManagerAndFlush(t *testing.T) {
	for _, build := range []struct {
		name string
		wrap func(agent.ConversationStore) agent.ConversationStore
	}{
		{name: "filter-window", wrap: func(inner agent.ConversationStore) agent.ConversationStore {
			return NewFilter(NewWindow(inner, 2))
		}},
		{name: "window-filter", wrap: func(inner agent.ConversationStore) agent.ConversationStore {
			return NewWindow(NewFilter(inner), 2)
		}},
	} {
		t.Run(build.name, func(t *testing.T) {
			inner := &flushingManager{InMemory: NewInMemory()}
			outer := build.wrap(inner)
			ctx := context.Background()
			revision, err := outer.Save(ctx, "conv", makeMessages(4), 0)
			if err != nil || revision != 1 {
				t.Fatalf("Save = %d, %v", revision, err)
			}
			snapshot, err := outer.Load(ctx, "conv")
			if err != nil || snapshot.Revision != revision {
				t.Fatalf("Load snapshot = %+v, %v", snapshot, err)
			}
			if _, err := outer.Save(ctx, "conv", makeMessages(2), 0); !errors.Is(err, agent.ErrConversationConflict) {
				t.Fatalf("stale Save = %v", err)
			}
			manager, ok := outer.(agent.ConversationManager)
			if !ok {
				t.Fatalf("%T does not compose ConversationManager", outer)
			}
			ids, err := manager.List(ctx)
			if err != nil || len(ids) != 1 || ids[0] != "conv" {
				t.Fatalf("List = %v, %v", ids, err)
			}
			flusher, ok := outer.(agent.Flusher)
			if !ok {
				t.Fatalf("%T does not compose Flusher", outer)
			}
			if err := flusher.Flush(ctx); err != nil || inner.flushes != 1 {
				t.Fatalf("Flush = %v, count=%d", err, inner.flushes)
			}
			if err := manager.Delete(ctx, "conv"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
