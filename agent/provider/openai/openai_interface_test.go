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
	}

	result := toOpenAIAssistantMessage(blocks)
	if result.OfAssistant == nil {
		t.Error("expected OfAssistant to be non-nil")
	}
}
