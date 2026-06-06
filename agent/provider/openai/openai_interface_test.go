package openai

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestToOpenAIUserMessages_MixedTypes_DoesNotPanic verifies that a mixed slice
// of ContentBlock values can be translated without panicking.
//
// Requirements: 1.1, 8.4
func TestToOpenAIUserMessages_MixedTypes_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("toOpenAIUserMessages panicked: %v", r)
		}
	}()

	blocks := []agent.ContentBlock{
		agent.TextBlock{Text: "before"},
		agent.ToolResultBlock{ToolUseID: "tc1", Content: "result"},
		agent.CacheableBlock{Inner: agent.TextBlock{Text: "cached text"}},
		agent.TextBlock{Text: "after"},
	}

	result := toOpenAIUserMessages(blocks)
	if len(result) == 0 {
		t.Error("expected non-empty translated result")
	}
}

// TestToOpenAIAssistantMessage_MixedTypes_DoesNotPanic verifies that a mixed
// slice of ContentBlock values in an assistant message can be translated
// without panicking.
//
// Requirements: 1.1, 8.4
func TestToOpenAIAssistantMessage_MixedTypes_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("toOpenAIAssistantMessage panicked: %v", r)
		}
	}()

	blocks := []agent.ContentBlock{
		agent.TextBlock{Text: "hello"},
		agent.CacheableBlock{Inner: agent.TextBlock{Text: "cached reply"}},
	}

	result := toOpenAIAssistantMessage(blocks)
	if result.OfAssistant == nil {
		t.Error("expected OfAssistant to be non-nil")
	}
}

// TestToOpenAIUserMessages_CacheableBlock_UnwrapsInner_DoesNotPanic verifies
// that CacheableBlock is transparently unwrapped to its inner block without
// panicking, and that the result is identical to translating the inner block
// directly.
//
// Requirements: 1.1, 8.4
func TestToOpenAIUserMessages_CacheableBlock_UnwrapsInner_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("toOpenAIUserMessages panicked on CacheableBlock: %v", r)
		}
	}()

	inner := agent.TextBlock{Text: "some text"}
	cacheable := agent.CacheableBlock{Inner: inner}

	directMsgs := toOpenAIUserMessages([]agent.ContentBlock{inner})
	wrappedMsgs := toOpenAIUserMessages([]agent.ContentBlock{cacheable})

	if len(directMsgs) != len(wrappedMsgs) {
		t.Errorf("CacheableBlock unwrap produced different message count: direct=%d, wrapped=%d",
			len(directMsgs), len(wrappedMsgs))
	}
}
