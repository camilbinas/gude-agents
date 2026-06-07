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
