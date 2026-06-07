package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
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
		agent.TextBlock{Text: "after"},
	}

	// Use a non-Claude model with caching disabled.
	result, err := toBedrockContentBlocks(blocks, "amazon.titan-text-express-v1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Error("expected non-empty translated result")
	}
}

// TestToBedrockContentBlocks_DocumentBlock_AutoCachePoint_Claude verifies that
// when injectCachePoints=true (Claude model + caching enabled), a CachePoint
// block is injected after the DocumentBlock.
//
// Requirements: 4.1, 4.2
func TestToBedrockContentBlocks_DocumentBlock_AutoCachePoint_Claude(t *testing.T) {
	pdfData := []byte("%PDF-1.0\n1 0 obj<</Type/Catalog>>endobj\n%%EOF")
	blocks := []agent.ContentBlock{
		agent.DocumentBlock{
			Source: agent.DocumentSource{
				Data:     pdfData,
				MIMEType: "application/pdf",
				Name:     "test.pdf",
			},
		},
		agent.TextBlock{Text: "question"},
	}

	// With injectCachePoints=true (Claude model, caching enabled).
	result, err := toBedrockContentBlocks(blocks, "anthropic.claude-3-5-haiku-20241022-v1:0", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: DocumentBlock, CachePoint, TextBlock = 3 blocks.
	if len(result) != 3 {
		t.Fatalf("expected 3 blocks (doc, cachepoint, text), got %d", len(result))
	}
	if _, ok := result[1].(*types.ContentBlockMemberCachePoint); !ok {
		t.Errorf("expected CachePoint at index 1, got %T", result[1])
	}
}

// TestToBedrockContentBlocks_DocumentBlock_NoCachePoint_NoCaching verifies that
// when injectCachePoints=false, no CachePoint is injected.
//
// Requirements: 4.1, 4.2
func TestToBedrockContentBlocks_DocumentBlock_NoCachePoint_NoCaching(t *testing.T) {
	pdfData := []byte("%PDF-1.0\n1 0 obj<</Type/Catalog>>endobj\n%%EOF")
	blocks := []agent.ContentBlock{
		agent.DocumentBlock{
			Source: agent.DocumentSource{
				Data:     pdfData,
				MIMEType: "application/pdf",
				Name:     "test.pdf",
			},
		},
		agent.TextBlock{Text: "question"},
	}

	result, err := toBedrockContentBlocks(blocks, "anthropic.claude-3-5-haiku-20241022-v1:0", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect: DocumentBlock, TextBlock = 2 blocks (no CachePoint).
	if len(result) != 2 {
		t.Fatalf("expected 2 blocks (doc, text), got %d", len(result))
	}
	for i, b := range result {
		if _, ok := b.(*types.ContentBlockMemberCachePoint); ok {
			t.Errorf("unexpected CachePoint at index %d", i)
		}
	}
}
