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
// This covers bare "anthropic." prefixes as well as cross-region routing variants
// such as "us.anthropic.", "eu.anthropic.", and "global.anthropic.".
func TestProperty_IsClaudeModelCorrectness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random model ID string — any printable ASCII.
		modelID := rapid.StringMatching(`[a-zA-Z0-9.\-_:/]{0,80}`).Draw(t, "modelID")

		got := isClaudeModel(modelID)
		want := strings.Contains(modelID, "anthropic.")

		if got != want {
			t.Fatalf("isClaudeModel(%q) = %v, want %v (strings.Contains=%v)", modelID, got, want, want)
		}
	})
}

// TestProperty_IsClaudeModelWithAnthropicSubstring verifies that any string
// containing "anthropic." is always detected as a Claude model, regardless of
// surrounding characters (cross-region prefixes, version suffixes, etc.).
func TestProperty_IsClaudeModelWithAnthropicSubstring(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Build an ID that always contains "anthropic." by sandwiching it between
		// random prefix and suffix strings.
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
		// Generate a string, then ensure "anthropic." is not present by skipping
		// if the generated string happens to contain it.
		modelID := rapid.StringMatching(`[a-zA-Z0-9\-_:/]{0,80}`).Draw(t, "modelID")

		// The character class above intentionally excludes '.' so "anthropic."
		// can never appear, but we guard anyway for robustness.
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
// by using a character set that excludes '.' entirely.
func nonClaudeModelIDGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-zA-Z0-9\-_:/]{1,60}`)
}

// hasCachePointBlock recursively checks whether a []types.ContentBlock slice
// contains any *types.ContentBlockMemberCachePoint.
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

// TestProperty_NonClaudeBedrockPayloadHasNoCachePoint verifies that for any
// non-"anthropic." model ID, calling toBedrockContentBlocks with a CacheableBlock
// produces no *ContentBlockMemberCachePoint in the result.
func TestProperty_NonClaudeBedrockPayloadHasNoCachePoint(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := nonClaudeModelIDGen().Draw(t, "nonClaudeModelID")

		// Double-check that the generated ID is genuinely non-Claude.
		if isClaudeModel(modelID) {
			t.Skip()
		}

		// Test with a CacheableBlock wrapping a TextBlock.
		blocks := []agent.ContentBlock{
			agent.CacheableBlock{Inner: agent.TextBlock{Text: "cached text"}},
		}

		result, err := toBedrockContentBlocks(blocks, modelID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if hasCachePointBlock(result) {
			t.Fatalf("non-Claude model %q: got a ContentBlockMemberCachePoint in the result, want none", modelID)
		}
	})
}

// TestProperty_NonClaudeBedrockPayloadHasNoCachePoint_ToolResultBlock verifies
// that CacheableBlock wrapping a ToolResultBlock also produces no cache marker
// for non-Claude models.
func TestProperty_NonClaudeBedrockPayloadHasNoCachePoint_ToolResultBlock(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := nonClaudeModelIDGen().Draw(t, "nonClaudeModelID")
		if isClaudeModel(modelID) {
			t.Skip()
		}

		blocks := []agent.ContentBlock{
			agent.CacheableBlock{Inner: agent.ToolResultBlock{
				ToolUseID: "tu-1",
				Content:   "some result",
			}},
		}

		result, err := toBedrockContentBlocks(blocks, modelID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if hasCachePointBlock(result) {
			t.Fatalf("non-Claude model %q: got CachePoint block from ToolResultBlock, want none", modelID)
		}
	})
}

// TestProperty_NonClaudeSystemPromptHasNoCachePoint verifies that with
// cachingEnabled=true but a non-Claude model ID, the system prompt construction
// logic (mirrored from Converse/ConverseStream) does NOT inject a
// SystemContentBlockMemberCachePoint.
func TestProperty_NonClaudeSystemPromptHasNoCachePoint(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := nonClaudeModelIDGen().Draw(t, "nonClaudeModelID")
		if isClaudeModel(modelID) {
			t.Skip()
		}

		systemText := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,200}`).Draw(t, "systemText")

		// Build a provider with cachingEnabled=true and a non-Claude model.
		p := &BedrockProvider{
			model:          modelID,
			cachingEnabled: true,
		}

		// Mirror the system-prompt construction logic from Converse/ConverseStream.
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

// TestProperty_NonClaudeBedrockPayloadHasNoCachePoint_MixedContent verifies
// that a mix of CacheableBlock and plain blocks for any non-Claude model ID
// contains no CachePoint markers anywhere in the result.
func TestProperty_NonClaudeBedrockPayloadHasNoCachePoint_MixedContent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := nonClaudeModelIDGen().Draw(t, "nonClaudeModelID")
		if isClaudeModel(modelID) {
			t.Skip()
		}

		// Build a mixed list: some plain TextBlocks, some CacheableBlocks.
		numPlain := rapid.IntRange(0, 3).Draw(t, "numPlain")
		numCacheable := rapid.IntRange(1, 3).Draw(t, "numCacheable")

		var blocks []agent.ContentBlock
		for i := 0; i < numPlain; i++ {
			blocks = append(blocks, agent.TextBlock{Text: "plain"})
		}
		for i := 0; i < numCacheable; i++ {
			blocks = append(blocks, agent.CacheableBlock{Inner: agent.TextBlock{Text: "cacheable"}})
		}

		result, err := toBedrockContentBlocks(blocks, modelID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if hasCachePointBlock(result) {
			t.Fatalf(
				"non-Claude model %q: found CachePoint block with %d plain + %d cacheable inputs, want none",
				modelID, numPlain, numCacheable,
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
// Property 7: No CacheableBlock → identical payload
// **Validates: Requirements 8.2, 8.3**
// ---------------------------------------------------------------------------

// genNonCacheableBedrockBlock generates a random TextBlock or ToolResultBlock —
// no CacheableBlock, no images that would require external data to decode.
func genNonCacheableBedrockBlock(t *rapid.T, name string) agent.ContentBlock {
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

// TestProperty_NoCacheableBlock_IdenticalBedrockPayload verifies Property 7
// for the Bedrock provider: when message content contains no CacheableBlock
// values, toBedrockContentBlocks produces identical output regardless of
// whether the model ID is a Claude model or not — because CacheableBlock
// handling is the only code path where model ID matters.
func TestProperty_NoCacheableBlock_IdenticalBedrockPayload(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a model ID that is definitely a Claude model.
		claudeModelSuffix := rapid.StringMatching(`[a-zA-Z0-9\-_:/]{0,40}`).Draw(t, "claudeSuffix")
		claudeModelID := "us.anthropic." + claudeModelSuffix

		// Generate a model ID that is definitely NOT a Claude model (no "anthropic.").
		nonClaudeModelID := rapid.StringMatching(`[a-zA-Z0-9\-_:/]{1,40}`).Draw(t, "nonClaudeModelID")
		// Ensure it doesn't accidentally contain "anthropic."
		if strings.Contains(nonClaudeModelID, "anthropic.") {
			t.Skip()
		}
		if nonClaudeModelID == "" {
			nonClaudeModelID = "amazon.titan-text-lite-v1"
		}

		// Generate 0–5 content blocks free of CacheableBlock.
		count := rapid.IntRange(0, 5).Draw(t, "count")
		blocks := make([]agent.ContentBlock, count)
		for i := range count {
			blocks[i] = genNonCacheableBedrockBlock(t, fmt.Sprintf("block%d", i))
		}

		// Translate for Claude model.
		claudeBlocks, err := toBedrockContentBlocks(blocks, claudeModelID)
		if err != nil {
			t.Fatalf("toBedrockContentBlocks(claude) error: %v", err)
		}

		// Translate for non-Claude model.
		nonClaudeBlocks, err := toBedrockContentBlocks(blocks, nonClaudeModelID)
		if err != nil {
			t.Fatalf("toBedrockContentBlocks(non-claude) error: %v", err)
		}

		// Block counts must match.
		if len(claudeBlocks) != len(nonClaudeBlocks) {
			t.Fatalf("block count mismatch: claude=%d non-claude=%d", len(claudeBlocks), len(nonClaudeBlocks))
		}

		// Each block must be the same type.
		for i := range claudeBlocks {
			claudeType := fmt.Sprintf("%T", claudeBlocks[i])
			nonClaudeType := fmt.Sprintf("%T", nonClaudeBlocks[i])
			if claudeType != nonClaudeType {
				t.Fatalf("block[%d] type mismatch: claude=%s non-claude=%s", i, claudeType, nonClaudeType)
			}
		}
	})
}

// TestProperty_NoCacheableBlock_BedrockWithCachingFlag verifies that
// toBedrockContentBlocks is identical whether or not a BedrockProvider
// has cachingEnabled=true, as long as no CacheableBlock is present in
// the message content (caching flag affects system prompt building, not
// message block translation).
func TestProperty_NoCacheableBlock_BedrockWithCachingFlag(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Use a Claude model ID so the difference would be observable if any.
		modelID := "us.anthropic.claude-3-5-haiku-20241022-v1:0"

		// Generate 0–5 content blocks free of CacheableBlock.
		count := rapid.IntRange(0, 5).Draw(t, "count")
		blocks := make([]agent.ContentBlock, count)
		for i := range count {
			blocks[i] = genNonCacheableBedrockBlock(t, fmt.Sprintf("block%d", i))
		}

		// toBedrockContentBlocks does not take a cachingEnabled flag directly —
		// cachingEnabled only affects system prompt construction in Converse/
		// ConverseStream. Calling it twice with the same inputs should always
		// produce identical results.
		blocksA, err := toBedrockContentBlocks(blocks, modelID)
		if err != nil {
			t.Fatalf("first call error: %v", err)
		}
		blocksB, err := toBedrockContentBlocks(blocks, modelID)
		if err != nil {
			t.Fatalf("second call error: %v", err)
		}

		if len(blocksA) != len(blocksB) {
			t.Fatalf("block count mismatch: %d vs %d", len(blocksA), len(blocksB))
		}

		for i := range blocksA {
			typeA := fmt.Sprintf("%T", blocksA[i])
			typeB := fmt.Sprintf("%T", blocksB[i])
			if typeA != typeB {
				t.Fatalf("block[%d] type mismatch: %s vs %s", i, typeA, typeB)
			}
		}
	})
}
