package memory

import (
	"context"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// Feature: memory-strategies, Property 1: Window round-trip preserves tail

// TestWindowRoundTripPreservesTail verifies that for any valid message slice
// and any positive integer N, saving then loading through Window returns a
// slice of length min(len(msgs), N) containing the last messages from the
// original slice in order.
//
// **Validates: Requirements 1.3, 1.4, 1.6**
func TestWindowRoundTripPreservesTail(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := genMessages(t)
		n := rapid.IntRange(1, 50).Draw(t, "windowSize")

		store := NewStore()
		win := NewWindow(store, n)
		ctx := context.Background()

		if err := win.Save(ctx, "conv", msgs); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := win.Load(ctx, "conv")
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		// Expected length is min(len(msgs), n)
		expectedLen := len(msgs)
		if n < expectedLen {
			expectedLen = n
		}

		if len(loaded) != expectedLen {
			t.Fatalf("expected %d messages, got %d (original=%d, n=%d)",
				expectedLen, len(loaded), len(msgs), n)
		}

		// Result must match the tail of the original slice
		if expectedLen > 0 {
			tail := msgs[len(msgs)-expectedLen:]
			if !reflect.DeepEqual(loaded, tail) {
				t.Fatalf("loaded messages do not match tail of original")
			}
		}
	})
}
