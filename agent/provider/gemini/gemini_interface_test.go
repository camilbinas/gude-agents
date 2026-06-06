package gemini

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestToGeminiParts_MixedTypes_DoesNotPanic verifies that a mixed slice of
// ContentBlock values can be translated without panicking.
//
// Requirements: 1.1, 8.4
func TestToGeminiParts_MixedTypes_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("toGeminiParts panicked: %v", r)
		}
	}()

	blocks := []agent.ContentBlock{
		agent.TextBlock{Text: "before"},
		agent.ToolResultBlock{ToolUseID: "tc1", Content: "result"},
		agent.CacheableBlock{Inner: agent.TextBlock{Text: "cached text"}},
		agent.TextBlock{Text: "after"},
	}

	result, err := toGeminiParts(blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Error("expected non-empty translated result")
	}
}

// TestToGeminiParts_CacheableBlock_UnwrapsInner_DoesNotPanic verifies that
// CacheableBlock is unwrapped to its inner block and both produce the same
// result, without panicking.
//
// Requirements: 1.1, 8.4
func TestToGeminiParts_CacheableBlock_UnwrapsInner_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("toGeminiParts panicked on CacheableBlock: %v", r)
		}
	}()

	inner := agent.TextBlock{Text: "some text"}
	cacheable := agent.CacheableBlock{Inner: inner}

	directParts, err := toGeminiParts([]agent.ContentBlock{inner})
	if err != nil {
		t.Fatalf("unexpected error translating inner: %v", err)
	}

	wrappedParts, err := toGeminiParts([]agent.ContentBlock{cacheable})
	if err != nil {
		t.Fatalf("unexpected error translating cacheable: %v", err)
	}

	// Both should produce the same number of parts.
	if len(directParts) != len(wrappedParts) {
		t.Errorf("CacheableBlock unwrap produced different part count: direct=%d, wrapped=%d",
			len(directParts), len(wrappedParts))
	}
}
