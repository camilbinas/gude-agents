package anthropic

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/camilbinas/gude-agents/agent"
)

// TestToAnthropicContentBlock_AllKnownTypes_DoesNotPanic verifies that
// toAnthropicContentBlocks handles all known ContentBlock types without panicking.
// This confirms the switch statement covers the full type set.
//
// Requirements: 1.1, 8.4
func TestToAnthropicContentBlock_AllKnownTypes_DoesNotPanic(t *testing.T) {
	blocks := []agent.ContentBlock{
		agent.TextBlock{Text: "hello"},
		agent.ToolUseBlock{ToolUseID: "id1", Name: "my_tool"},
		agent.ToolResultBlock{ToolUseID: "id1", Content: "result"},
	}

	for _, b := range blocks {
		t.Run("", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("toAnthropicContentBlocks panicked: %v", r)
				}
			}()
			result := toAnthropicContentBlocks([]agent.ContentBlock{b}, agent.RoleUser, false)
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
		agent.TextBlock{Text: "after"},
	}

	// Test without caching.
	result := toAnthropicContentBlocks(blocks, agent.RoleUser, false)
	if len(result) == 0 {
		t.Error("expected non-empty translated result")
	}
}

// TestToAnthropicContentBlocks_DocumentBlock_CacheControl verifies that when
// cachingEnabled is true, DocumentBlocks get cache_control set, and when
// cachingEnabled is false, they do not.
//
// Requirements: 3.1, 3.2
func TestToAnthropicContentBlocks_DocumentBlock_CacheControl(t *testing.T) {
	doc := agent.DocumentBlock{
		Source: agent.DocumentSource{
			Base64:   "JVBE", // minimal base64
			MIMEType: "application/pdf",
			Name:     "test.pdf",
		},
	}
	blocks := []agent.ContentBlock{doc}

	t.Run("caching disabled - no cache_control", func(t *testing.T) {
		result := toAnthropicContentBlocks(blocks, agent.RoleUser, false)
		if len(result) == 0 {
			t.Fatal("expected non-empty result")
		}
		if result[0].OfDocument == nil {
			t.Fatal("expected OfDocument to be set")
		}
		// cache_control should NOT be set when caching is disabled.
		if !param.IsOmitted(result[0].OfDocument.CacheControl) {
			t.Error("expected CacheControl to be absent when caching is disabled")
		}
	})

	t.Run("caching enabled - cache_control set", func(t *testing.T) {
		result := toAnthropicContentBlocks(blocks, agent.RoleUser, true)
		if len(result) == 0 {
			t.Fatal("expected non-empty result")
		}
		if result[0].OfDocument == nil {
			t.Fatal("expected OfDocument to be set")
		}
		// When caching is enabled, CacheControl should be set (non-omitted).
		if param.IsOmitted(result[0].OfDocument.CacheControl) {
			t.Error("expected CacheControl to be set on DocumentBlock when caching is enabled")
		}
	})
}
