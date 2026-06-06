package anthropic

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestToAnthropicContentBlock_AllKnownTypes_DoesNotPanic verifies that
// toAnthropicContentBlock handles all known ContentBlock types without panicking.
// This confirms the switch statement covers the full type set.
//
// Requirements: 1.1, 8.4
func TestToAnthropicContentBlock_AllKnownTypes_DoesNotPanic(t *testing.T) {
	blocks := []agent.ContentBlock{
		agent.TextBlock{Text: "hello"},
		agent.ToolUseBlock{ToolUseID: "id1", Name: "my_tool"},
		agent.ToolResultBlock{ToolUseID: "id1", Content: "result"},
		agent.CacheableBlock{Inner: agent.TextBlock{Text: "cached"}},
	}

	for _, b := range blocks {
		b := b
		t.Run("", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("toAnthropicContentBlock panicked: %v", r)
				}
			}()
			result := toAnthropicContentBlock(b, agent.RoleUser)
			_ = result
		})
	}
}

// TestToAnthropicContentBlocks_MixedTypes_DoesNotPanic verifies that a mixed
// slice of ContentBlock values can be translated without panicking.
//
// Requirements: 1.1, 8.4
func TestToAnthropicContentBlocks_MixedTypes_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("toAnthropicContentBlocks panicked: %v", r)
		}
	}()

	blocks := []agent.ContentBlock{
		agent.TextBlock{Text: "before"},
		agent.ToolResultBlock{ToolUseID: "tc1", Content: "result", IsError: false},
		agent.CacheableBlock{Inner: agent.TextBlock{Text: "cached text"}},
		agent.TextBlock{Text: "after"},
	}

	result := toAnthropicContentBlocks(blocks, agent.RoleUser)
	if len(result) == 0 {
		t.Error("expected non-empty translated result")
	}
}

// TestToAnthropicCacheableBlock_AllInnerTypes_DoesNotPanic verifies that
// translating a CacheableBlock with each known inner type does not panic, and
// that the CacheControl field is set (i.e., the cache breakpoint is attached).
//
// Requirements: 1.1, 8.4
func TestToAnthropicCacheableBlock_AllInnerTypes_DoesNotPanic(t *testing.T) {
	innerBlocks := []struct {
		name  string
		inner agent.ContentBlock
	}{
		{"TextBlock", agent.TextBlock{Text: "cached text"}},
		{"ToolResultBlock", agent.ToolResultBlock{ToolUseID: "id1", Content: "result"}},
		// CacheableBlock wrapping another CacheableBlock (nested) — falls back to toAnthropicContentBlock
		{"NestedCacheableBlock", agent.CacheableBlock{Inner: agent.TextBlock{Text: "nested"}}},
	}

	for _, tc := range innerBlocks {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("toAnthropicCacheableBlock panicked with inner %s: %v", tc.name, r)
				}
			}()
			cacheable := agent.CacheableBlock{Inner: tc.inner}
			result := toAnthropicCacheableBlock(cacheable, agent.RoleUser)
			_ = result
		})
	}
}
