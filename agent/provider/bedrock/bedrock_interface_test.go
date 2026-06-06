package bedrock

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestToBedrockContentBlocks_MixedTypes_DoesNotPanic verifies that a mixed slice
// of ContentBlock values can be translated without panicking.
//
// Requirements: 1.1, 8.4
func TestToBedrockContentBlocks_MixedTypes_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("toBedrockContentBlocks panicked: %v", r)
		}
	}()

	blocks := []agent.ContentBlock{
		agent.TextBlock{Text: "before"},
		agent.ToolResultBlock{ToolUseID: "tc1", Content: "result"},
		agent.CacheableBlock{Inner: agent.TextBlock{Text: "cached text"}},
		agent.TextBlock{Text: "after"},
	}

	// Use a non-Claude model to keep the test simple.
	result, err := toBedrockContentBlocks(blocks, "amazon.titan-text-express-v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Error("expected non-empty translated result")
	}
}

// TestToBedrockCacheableBlock_DoesNotPanic_NonClaude verifies that translating
// a CacheableBlock on a non-Claude model does not panic and simply unwraps
// the inner block.
//
// Requirements: 1.1, 8.4
func TestToBedrockCacheableBlock_DoesNotPanic_NonClaude(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("toBedrockCacheableBlock panicked on non-Claude model: %v", r)
		}
	}()

	cacheable := agent.CacheableBlock{Inner: agent.TextBlock{Text: "hello"}}
	result, err := toBedrockCacheableBlock(cacheable, "amazon.titan-text-express-v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Error("expected non-empty result for non-Claude CacheableBlock")
	}
}

// TestToBedrockCacheableBlock_DoesNotPanic_Claude verifies that translating a
// CacheableBlock on a Claude model does not panic and appends a CachePoint
// block after the inner block.
//
// Requirements: 1.1, 8.4
func TestToBedrockCacheableBlock_DoesNotPanic_Claude(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("toBedrockCacheableBlock panicked on Claude model: %v", r)
		}
	}()

	cacheable := agent.CacheableBlock{Inner: agent.TextBlock{Text: "hello"}}
	result, err := toBedrockCacheableBlock(cacheable, "anthropic.claude-3-5-haiku-20241022-v1:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have the inner text block plus a CachePoint sentinel.
	if len(result) < 2 {
		t.Errorf("expected at least 2 blocks (inner + CachePoint), got %d", len(result))
	}
}
