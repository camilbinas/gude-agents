package conversation

import (
	"context"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)


// TestWindowRoundTripPreservesTail verifies that for any valid message slice
// and any positive integer N, saving then loading through Window returns a
// slice whose length is at most N, whose messages are a suffix of the original
// slice, and which contains no orphaned tool_result blocks.
//
func TestWindowRoundTripPreservesTail(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := genMessages(t)
		n := rapid.IntRange(1, 50).Draw(t, "windowSize")

		store := NewInMemory()
		win := NewWindow(store, n)
		ctx := context.Background()

		if err := win.Save(ctx, "conv", msgs); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := win.Load(ctx, "conv")
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		// Must not exceed the window size.
		if len(loaded) > n {
			t.Fatalf("loaded %d messages, exceeds window size %d", len(loaded), n)
		}

		// Must be a suffix of the original slice.
		if len(loaded) > 0 {
			offset := len(msgs) - len(loaded)
			if offset < 0 {
				t.Fatalf("loaded more messages (%d) than original (%d)", len(loaded), len(msgs))
			}
			if !reflect.DeepEqual(loaded, msgs[offset:]) {
				t.Fatalf("loaded messages are not a suffix of the original slice")
			}
		}

		// Must contain no orphaned tool_result blocks — where "orphaned" means
		// the tool_use existed somewhere in the original slice but was cut off.
		// (tool_results with IDs that never had a tool_use in the original are
		// pre-existing invalid data and are not introduced by truncation.)
		presentInOriginal := make(map[string]bool)
		for _, m := range msgs {
			for _, b := range m.Content {
				if tu, ok := b.(agent.ToolUseBlock); ok {
					presentInOriginal[tu.ToolUseID] = true
				}
			}
		}
		presentInLoaded := make(map[string]bool)
		for _, m := range loaded {
			for _, b := range m.Content {
				if tu, ok := b.(agent.ToolUseBlock); ok {
					presentInLoaded[tu.ToolUseID] = true
				}
			}
		}
		for _, m := range loaded {
			for _, b := range m.Content {
				if tr, ok := b.(agent.ToolResultBlock); ok {
					// Only flag as orphaned if the tool_use existed in the original
					// (meaning truncation removed it) but is missing from loaded.
					if presentInOriginal[tr.ToolUseID] && !presentInLoaded[tr.ToolUseID] {
						t.Fatalf("truncation orphaned tool_result id=%q (tool_use existed in original but was cut)", tr.ToolUseID)
					}
				}
			}
		}
	})
}

// TestWindowSafeTruncate_NoOrphanedToolResults verifies that safeTruncate never
// returns a slice containing a tool_result whose tool_use_id is not present in
// the same slice.
func TestWindowSafeTruncate_NoOrphanedToolResults(t *testing.T) {
	msg := func(role string, blocks ...agent.ContentBlock) agent.Message {
		return agent.Message{Role: agent.Role(role), Content: blocks}
	}

	// Build a conversation with two tool call/result pairs, then a plain turn.
	//
	//  [0] user: "query 1"
	//  [1] assistant: tool_use id=tu1
	//  [2] user: tool_result id=tu1
	//  [3] assistant: "answer 1"
	//  [4] user: "query 2"
	//  [5] assistant: tool_use id=tu2
	//  [6] user: tool_result id=tu2
	//  [7] assistant: "answer 2"
	msgs := []agent.Message{
		msg("user", agent.TextBlock{Text: "query 1"}),
		msg("assistant", agent.ToolUseBlock{ToolUseID: "tu1", Name: "run_query"}),
		msg("user", agent.ToolResultBlock{ToolUseID: "tu1", Content: "result 1"}),
		msg("assistant", agent.TextBlock{Text: "answer 1"}),
		msg("user", agent.TextBlock{Text: "query 2"}),
		msg("assistant", agent.ToolUseBlock{ToolUseID: "tu2", Name: "run_query"}),
		msg("user", agent.ToolResultBlock{ToolUseID: "tu2", Content: "result 2"}),
		msg("assistant", agent.TextBlock{Text: "answer 2"}),
	}

	// A window of 5 would naively cut at index 3, leaving tool_result tu2 without tu2's tool_use.
	// Actually with 8 msgs and n=5, naive start=3 → msgs[3:] which is fine.
	// With n=4, naive start=4 → msgs[4:] which is also fine.
	// With n=3, naive start=5 → msgs[5:] = [tool_use tu2, tool_result tu2, answer] — fine.
	// With n=2, naive start=6 → msgs[6:] = [tool_result tu2, answer] — ORPHANED tu2.
	// safeTruncate should advance to start=7 → [answer 2].

	result := safeTruncate(msgs, 6)

	// Verify no orphaned tool_results.
	present := make(map[string]bool)
	for _, m := range result {
		for _, b := range m.Content {
			if tu, ok := b.(agent.ToolUseBlock); ok {
				present[tu.ToolUseID] = true
			}
		}
	}
	for _, m := range result {
		for _, b := range m.Content {
			if tr, ok := b.(agent.ToolResultBlock); ok {
				if !present[tr.ToolUseID] {
					t.Errorf("orphaned tool_result with id=%q in truncated slice", tr.ToolUseID)
				}
			}
		}
	}

	// The result should start at index 7 (the "answer 2" message).
	if len(result) != 1 {
		t.Errorf("expected 1 message after safe truncation, got %d", len(result))
	}
}
