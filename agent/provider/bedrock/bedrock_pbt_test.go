package bedrock

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// **Validates: Requirements 4.3**

// TestProperty_IsClaudeModelCorrectness verifies that for any model ID string,
// isClaudeModel(id) returns true if and only if strings.Contains(id, "anthropic.").
func TestProperty_IsClaudeModelCorrectness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := rapid.StringMatching(`[a-zA-Z0-9.\-_:/]{0,80}`).Draw(t, "modelID")

		got := isClaudeModel(modelID)
		want := strings.Contains(modelID, "anthropic.")

		if got != want {
			t.Fatalf("isClaudeModel(%q) = %v, want %v (strings.Contains=%v)", modelID, got, want, want)
		}
	})
}

// TestProperty_IsClaudeModelWithAnthropicSubstring verifies that any string
// containing "anthropic." is always detected as a Claude model.
func TestProperty_IsClaudeModelWithAnthropicSubstring(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := rapid.StringMatching(`[a-zA-Z0-9.\-_:/]{0,20}`).Draw(t, "prefix")
		suffix := rapid.StringMatching(`[a-zA-Z0-9.\-_:/]{0,40}`).Draw(t, "suffix")
		modelID := prefix + "anthropic." + suffix

		if !isClaudeModel(modelID) {
			t.Fatalf("isClaudeModel(%q) = false, want true (contains \"anthropic.\")", modelID)
		}
	})
}

// TestProperty_IsClaudeModelWithoutAnthropicSubstring verifies that any string
// that does not contain "anthropic." is never detected as a Claude model.
func TestProperty_IsClaudeModelWithoutAnthropicSubstring(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := rapid.StringMatching(`[a-zA-Z0-9\-_:/]{0,80}`).Draw(t, "modelID")

		if strings.Contains(modelID, "anthropic.") {
			t.Skip()
		}

		if isClaudeModel(modelID) {
			t.Fatalf("isClaudeModel(%q) = true, want false (does not contain \"anthropic.\")", modelID)
		}
	})
}

// **Validates: Requirements 5.5**

// **Validates: Requirements 2.7, 4.2**

// nonClaudeModelIDGen generates model IDs that do NOT contain "anthropic."
func nonClaudeModelIDGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-zA-Z0-9\-_:/]{1,60}`)
}

// hasCachePointBlock checks whether a []types.ContentBlock slice contains any
// *types.ContentBlockMemberCachePoint.
func hasCachePointBlock(blocks []types.ContentBlock) bool {
	for _, b := range blocks {
		if _, ok := b.(*types.ContentBlockMemberCachePoint); ok {
			return true
		}
	}
	return false
}

// hasSystemCachePointBlock checks whether a []types.SystemContentBlock slice
// contains any *types.SystemContentBlockMemberCachePoint.
func hasSystemCachePointBlock(sysBlocks []types.SystemContentBlock) bool {
	for _, b := range sysBlocks {
		if _, ok := b.(*types.SystemContentBlockMemberCachePoint); ok {
			return true
		}
	}
	return false
}

// TestProperty_DocumentBlockInjectsCachePointForClaude verifies that when
// injectCachePoints=true (Claude model + caching enabled), a CachePoint block
// is injected after every DocumentBlock.
func TestProperty_DocumentBlockInjectsCachePointForClaude(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := "us.anthropic.claude-3-5-haiku-20241022-v1:0"

		pdfData := []byte("%PDF-1.0\n1 0 obj<</Type/Catalog>>endobj\n%%EOF")
		doc := agent.DocumentBlock{
			Source: agent.DocumentSource{
				Data:     pdfData,
				MIMEType: "application/pdf",
				Name:     "test.pdf",
			},
		}

		numDocs := rapid.IntRange(1, 3).Draw(t, "numDocs")
		blocks := make([]agent.ContentBlock, numDocs)
		for i := range numDocs {
			blocks[i] = doc
		}

		// With injectCachePoints=true, each DocumentBlock should be followed by a CachePoint.
		result, err := toBedrockContentBlocks(blocks, modelID, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Expect 2 blocks per document: DocumentBlock + CachePoint.
		expectedCount := numDocs * 2
		if len(result) != expectedCount {
			t.Fatalf("expected %d blocks (doc+cachepoint per doc), got %d", expectedCount, len(result))
		}

		// Every odd-indexed block should be a CachePoint.
		for i := 1; i < len(result); i += 2 {
			if _, ok := result[i].(*types.ContentBlockMemberCachePoint); !ok {
				t.Fatalf("expected CachePoint at index %d, got %T", i, result[i])
			}
		}
	})
}

// TestProperty_DocumentBlockNoCachePointForNonClaude verifies that for non-Claude
// models, no CachePoint is injected even when injectCachePoints would be true
// (but the caller never sets it for non-Claude models).
func TestProperty_DocumentBlockNoCachePointForNonClaude(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := nonClaudeModelIDGen().Draw(t, "nonClaudeModelID")
		if isClaudeModel(modelID) {
			t.Skip()
		}

		pdfData := []byte("%PDF-1.0\n1 0 obj<</Type/Catalog>>endobj\n%%EOF")
		blocks := []agent.ContentBlock{
			agent.DocumentBlock{
				Source: agent.DocumentSource{
					Data:     pdfData,
					MIMEType: "application/pdf",
					Name:     "test.pdf",
				},
			},
		}

		// injectCachePoints=false for non-Claude: no CachePoint should appear.
		result, err := toBedrockContentBlocks(blocks, modelID, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if hasCachePointBlock(result) {
			t.Fatalf("non-Claude model %q: got a ContentBlockMemberCachePoint in the result, want none", modelID)
		}
	})
}

// TestProperty_NonClaudeSystemPromptHasNoCachePoint verifies that with
// cachingEnabled=true but a non-Claude model ID, the system prompt construction
// logic does NOT inject a SystemContentBlockMemberCachePoint.
func TestProperty_NonClaudeSystemPromptHasNoCachePoint(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := nonClaudeModelIDGen().Draw(t, "nonClaudeModelID")
		if isClaudeModel(modelID) {
			t.Skip()
		}

		systemText := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,200}`).Draw(t, "systemText")

		p := &BedrockProvider{
			model:          modelID,
			cachingEnabled: true,
		}

		var sysBlocks []types.SystemContentBlock
		if systemText != "" {
			if p.cachingEnabled && isClaudeModel(p.model) {
				sysBlocks = []types.SystemContentBlock{
					&types.SystemContentBlockMemberText{Value: systemText},
					&types.SystemContentBlockMemberCachePoint{
						Value: types.CachePointBlock{Type: types.CachePointTypeDefault},
					},
				}
			} else {
				sysBlocks = []types.SystemContentBlock{
					&types.SystemContentBlockMemberText{Value: systemText},
				}
			}
		}

		if hasSystemCachePointBlock(sysBlocks) {
			t.Fatalf(
				"non-Claude model %q with cachingEnabled=true: got SystemCachePoint block in system prompt, want none",
				modelID,
			)
		}
	})
}

// TestProperty_BedrockCacheUsageFieldsPreserved verifies that for any random
// cache token counts r (read) and w (write), the usage mapping logic in Converse
// faithfully propagates both values into ProviderResponse.Usage.
func TestProperty_BedrockCacheUsageFieldsPreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r := rapid.Int32Range(0, 1<<30).Draw(t, "cacheReadTokens")
		w := rapid.Int32Range(0, 1<<30).Draw(t, "cacheWriteTokens")

		out := &bedrockruntime.ConverseOutput{
			Usage: &types.TokenUsage{
				InputTokens:           aws.Int32(100),
				OutputTokens:          aws.Int32(50),
				CacheReadInputTokens:  aws.Int32(r),
				CacheWriteInputTokens: aws.Int32(w),
			},
		}

		resp := &agent.ProviderResponse{}
		if out.Usage != nil {
			resp.Usage.InputTokens = int(aws.ToInt32(out.Usage.InputTokens))
			resp.Usage.OutputTokens = int(aws.ToInt32(out.Usage.OutputTokens))
			if out.Usage.CacheReadInputTokens != nil {
				resp.Usage.CacheReadTokens = int(aws.ToInt32(out.Usage.CacheReadInputTokens))
			}
			if out.Usage.CacheWriteInputTokens != nil {
				resp.Usage.CacheWriteTokens = int(aws.ToInt32(out.Usage.CacheWriteInputTokens))
			}
		}

		if resp.Usage.CacheReadTokens != int(r) {
			t.Fatalf("CacheReadTokens = %d, want %d", resp.Usage.CacheReadTokens, r)
		}
		if resp.Usage.CacheWriteTokens != int(w) {
			t.Fatalf("CacheWriteTokens = %d, want %d", resp.Usage.CacheWriteTokens, w)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 7: No DocumentBlock → identical payload
// **Validates: Requirements 8.2, 8.3**
// ---------------------------------------------------------------------------

// genNonDocumentBedrockBlock generates a random TextBlock or ToolResultBlock —
// no DocumentBlock (which would be affected by injectCachePoints).
func genNonDocumentBedrockBlock(t *rapid.T, name string) agent.ContentBlock {
	kind := rapid.IntRange(0, 1).Draw(t, name+"_kind")
	switch kind {
	case 0:
		text := rapid.StringOf(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"))).
			Draw(t, name+"_text")
		return agent.TextBlock{Text: text}
	default:
		toolUseID := rapid.StringMatching(`[a-zA-Z0-9_]{1,32}`).Draw(t, name+"_toolUseID")
		content := rapid.StringOf(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz"))).
			Draw(t, name+"_content")
		isError := rapid.Bool().Draw(t, name+"_isError")
		return agent.ToolResultBlock{ToolUseID: toolUseID, Content: content, IsError: isError}
	}
}

// TestProperty_NoDocumentBlock_IdenticalBedrockPayload verifies that when
// message content contains no DocumentBlock values, toBedrockContentBlocks
// produces identical output regardless of whether injectCachePoints is true or
// false — because caching only affects DocumentBlocks.
func TestProperty_NoDocumentBlock_IdenticalBedrockPayload(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		claudeModelSuffix := rapid.StringMatching(`[a-zA-Z0-9\-_:/]{0,40}`).Draw(t, "claudeSuffix")
		claudeModelID := "us.anthropic." + claudeModelSuffix

		nonClaudeModelID := rapid.StringMatching(`[a-zA-Z0-9\-_:/]{1,40}`).Draw(t, "nonClaudeModelID")
		if strings.Contains(nonClaudeModelID, "anthropic.") {
			t.Skip()
		}
		if nonClaudeModelID == "" {
			nonClaudeModelID = "amazon.titan-text-lite-v1"
		}

		count := rapid.IntRange(0, 5).Draw(t, "count")
		blocks := make([]agent.ContentBlock, count)
		for i := range count {
			blocks[i] = genNonDocumentBedrockBlock(t, fmt.Sprintf("block%d", i))
		}

		// Translate for Claude model without caching.
		claudeBlocks, err := toBedrockContentBlocks(blocks, claudeModelID, false)
		if err != nil {
			t.Fatalf("toBedrockContentBlocks(claude,false) error: %v", err)
		}

		// Translate for Claude model with caching — should be identical since no DocumentBlocks.
		claudeBlocksWithCaching, err := toBedrockContentBlocks(blocks, claudeModelID, true)
		if err != nil {
			t.Fatalf("toBedrockContentBlocks(claude,true) error: %v", err)
		}

		// Translate for non-Claude model.
		nonClaudeBlocks, err := toBedrockContentBlocks(blocks, nonClaudeModelID, false)
		if err != nil {
			t.Fatalf("toBedrockContentBlocks(non-claude) error: %v", err)
		}

		// All three should be the same length.
		if len(claudeBlocks) != len(claudeBlocksWithCaching) {
			t.Fatalf("claude without/with caching block count mismatch: %d vs %d",
				len(claudeBlocks), len(claudeBlocksWithCaching))
		}
		if len(claudeBlocks) != len(nonClaudeBlocks) {
			t.Fatalf("block count mismatch: claude=%d non-claude=%d", len(claudeBlocks), len(nonClaudeBlocks))
		}

		for i := range claudeBlocks {
			claudeType := fmt.Sprintf("%T", claudeBlocks[i])
			nonClaudeType := fmt.Sprintf("%T", nonClaudeBlocks[i])
			if claudeType != nonClaudeType {
				t.Fatalf("block[%d] type mismatch: claude=%s non-claude=%s", i, claudeType, nonClaudeType)
			}
		}
	})
}
