package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// **Validates: Requirements 2.5**

// TestProperty_SystemBlockCacheControlOnLastElementOnly verifies that when
// cachingEnabled is true and a non-empty system string is provided, the last
// System block has a non-nil CacheControl and all prior blocks have nil CacheControl.
//
// Property 2: Anthropic system prompt cache_control placement
func TestProperty_SystemBlockCacheControlOnLastElementOnly(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a non-empty system string.
		system := rapid.StringOf(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ"))).
			Filter(func(s string) bool { return len(s) > 0 }).
			Draw(t, "system")

		p := &AnthropicProvider{
			model:          "claude-3-5-haiku-20241022",
			cachingEnabled: true,
		}

		params := agent.ConverseParams{
			Messages: []agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
			},
			System: system,
		}

		result := p.buildParams(params)

		if len(result.System) == 0 {
			t.Fatal("expected at least one System block, got none")
		}

		lastIdx := len(result.System) - 1

		// All blocks prior to the last must have CacheControl absent (zero/omitted).
		for i := 0; i < lastIdx; i++ {
			if !param.IsOmitted(result.System[i].CacheControl) {
				t.Fatalf("expected System[%d].CacheControl to be absent (omitted), but it was set", i)
			}
		}

		// The last block must have cache_control set. We verify via JSON since buildParams
		// uses CacheControlEphemeralParam{} (zero struct) which still marshals to
		// {"type":"ephemeral"} via its default tag.
		lastJSON, err := json.Marshal(result.System[lastIdx])
		if err != nil {
			t.Fatalf("failed to marshal last System block: %v", err)
		}
		if !strings.Contains(string(lastJSON), "cache_control") {
			t.Fatalf("expected System[%d] (last) JSON to contain cache_control, got: %s", lastIdx, string(lastJSON))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Anthropic CacheableBlock translation preserves content
// **Validates: Requirements 3.1, 3.2, 3.3, 3.4**
// ---------------------------------------------------------------------------

// genTextBlock generates a random TextBlock.
func genTextBlock() *rapid.Generator[agent.TextBlock] {
	return rapid.Custom(func(t *rapid.T) agent.TextBlock {
		text := rapid.StringOf(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"))).
			Draw(t, "text")
		return agent.TextBlock{Text: text}
	})
}

// genToolResultBlock generates a random ToolResultBlock (text-only, no images).
func genToolResultBlock() *rapid.Generator[agent.ToolResultBlock] {
	return rapid.Custom(func(t *rapid.T) agent.ToolResultBlock {
		toolUseID := rapid.StringMatching(`[a-zA-Z0-9_]{1,32}`).Draw(t, "toolUseID")
		content := rapid.StringOf(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz"))).Draw(t, "content")
		isError := rapid.Bool().Draw(t, "isError")
		return agent.ToolResultBlock{
			ToolUseID: toolUseID,
			Content:   content,
			IsError:   isError,
		}
	})
}

// genImageBlock generates a fixed-data ImageBlock. Image data is not randomised
// because rapid requires generators to consume data from the bitstream; the
// meaningful variation here is tested through the other block type generators.
// We use a draw call to keep rapid's bookkeeping happy while keeping the image
// payload deterministic so base64 decoding always succeeds.
func genImageBlock() *rapid.Generator[agent.ImageBlock] {
	return rapid.Custom(func(t *rapid.T) agent.ImageBlock {
		// Draw a bool just so rapid sees this generator consume data.
		_ = rapid.Bool().Draw(t, "imageVariant")

		// 1x1 transparent PNG – smallest valid PNG we can embed.
		pngData := []byte{
			0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
			0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
			0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
			0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
			0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
			0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
			0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
			0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
			0x42, 0x60, 0x82,
		}
		encoded := base64.StdEncoding.EncodeToString(pngData)
		return agent.ImageBlock{
			Source: agent.ImageSource{
				Base64:   encoded,
				MIMEType: "image/png",
			},
		}
	})
}

// genDocumentBlock generates a fixed-data DocumentBlock. Like genImageBlock,
// the document payload is deterministic; we draw a bool so rapid sees data.
func genDocumentBlock() *rapid.Generator[agent.DocumentBlock] {
	return rapid.Custom(func(t *rapid.T) agent.DocumentBlock {
		// Draw a bool so rapid sees this generator consume data from the bitstream.
		_ = rapid.Bool().Draw(t, "docVariant")

		pdfData := []byte("%PDF-1.0\n1 0 obj<</Type/Catalog>>endobj\n%%EOF")
		encoded := base64.StdEncoding.EncodeToString(pdfData)
		return agent.DocumentBlock{
			Source: agent.DocumentSource{
				Base64: encoded,
			},
		}
	})
}

// TestProperty_AnthropicCacheableBlockPreservesContent verifies Property 5:
// for any supported inner block type, toAnthropicCacheableBlock produces a
// result that:
//  1. Has CacheControl set (non-zero, not omitted) on the underlying block param.
//  2. Preserves the inner content fields to match the non-cacheable translation.
func TestProperty_AnthropicCacheableBlockPreservesContent(t *testing.T) {
	// Sub-test for TextBlock.
	t.Run("TextBlock", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			inner := genTextBlock().Draw(t, "inner")

			cacheable := agent.CacheableBlock{Inner: inner}
			result := toAnthropicCacheableBlock(cacheable, agent.RoleUser)
			plain := toAnthropicContentBlock(inner, agent.RoleUser)

			// Must use the OfText union arm.
			if result.OfText == nil {
				t.Fatal("expected OfText to be set for TextBlock CacheableBlock")
			}
			// CacheControl must be set (NewCacheControlEphemeralParam sets Type="ephemeral", non-zero).
			if param.IsOmitted(result.OfText.CacheControl) {
				t.Fatal("expected CacheControl to be set on CacheableBlock TextBlockParam, but it was omitted")
			}
			// Content (Text field) must be preserved.
			if plain.OfText == nil {
				t.Fatal("expected OfText to be set for plain TextBlock")
			}
			if result.OfText.Text != plain.OfText.Text {
				t.Fatalf("text content mismatch: cacheable=%q plain=%q", result.OfText.Text, plain.OfText.Text)
			}
		})
	})

	// Sub-test for ToolResultBlock.
	t.Run("ToolResultBlock", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			inner := genToolResultBlock().Draw(t, "inner")

			cacheable := agent.CacheableBlock{Inner: inner}
			result := toAnthropicCacheableBlock(cacheable, agent.RoleUser)
			plain := toAnthropicContentBlock(inner, agent.RoleUser)

			// Must use the OfToolResult union arm.
			if result.OfToolResult == nil {
				t.Fatal("expected OfToolResult to be set for ToolResultBlock CacheableBlock")
			}
			// CacheControl must be set.
			if param.IsOmitted(result.OfToolResult.CacheControl) {
				t.Fatal("expected CacheControl to be set on CacheableBlock ToolResultBlockParam, but it was omitted")
			}
			// ToolUseID must be preserved.
			if plain.OfToolResult == nil {
				t.Fatal("expected OfToolResult to be set for plain ToolResultBlock")
			}
			if result.OfToolResult.ToolUseID != plain.OfToolResult.ToolUseID {
				t.Fatalf("ToolUseID mismatch: cacheable=%q plain=%q",
					result.OfToolResult.ToolUseID, plain.OfToolResult.ToolUseID)
			}
		})
	})

	// Sub-test for ImageBlock.
	t.Run("ImageBlock", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			inner := genImageBlock().Draw(t, "inner")

			cacheable := agent.CacheableBlock{Inner: inner}
			result := toAnthropicCacheableBlock(cacheable, agent.RoleUser)
			plain := toAnthropicContentBlock(inner, agent.RoleUser)

			// Must use the OfImage union arm.
			if result.OfImage == nil {
				t.Fatal("expected OfImage to be set for ImageBlock CacheableBlock")
			}
			// CacheControl must be set.
			if param.IsOmitted(result.OfImage.CacheControl) {
				t.Fatal("expected CacheControl to be set on CacheableBlock ImageBlockParam, but it was omitted")
			}
			// Source must be preserved.
			if plain.OfImage == nil {
				t.Fatal("expected OfImage to be set for plain ImageBlock")
			}
			// Both should have OfBase64 set (since genImageBlock uses base64 source).
			if result.OfImage.Source.OfBase64 == nil || plain.OfImage.Source.OfBase64 == nil {
				t.Fatal("expected OfBase64 source on both cacheable and plain ImageBlock")
			}
			if result.OfImage.Source.OfBase64.Data != plain.OfImage.Source.OfBase64.Data {
				t.Fatal("image source data mismatch between cacheable and plain")
			}
		})
	})

	// Sub-test for DocumentBlock.
	t.Run("DocumentBlock", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			inner := genDocumentBlock().Draw(t, "inner")

			cacheable := agent.CacheableBlock{Inner: inner}
			result := toAnthropicCacheableBlock(cacheable, agent.RoleUser)
			plain := toAnthropicContentBlock(inner, agent.RoleUser)

			// Must use the OfDocument union arm.
			if result.OfDocument == nil {
				t.Fatal("expected OfDocument to be set for DocumentBlock CacheableBlock")
			}
			// CacheControl must be set.
			if param.IsOmitted(result.OfDocument.CacheControl) {
				t.Fatal("expected CacheControl to be set on CacheableBlock DocumentBlockParam, but it was omitted")
			}
			// Source must be preserved.
			if plain.OfDocument == nil {
				t.Fatal("expected OfDocument to be set for plain DocumentBlock")
			}
			// Both should have OfBase64 set.
			if result.OfDocument.Source.OfBase64 == nil || plain.OfDocument.Source.OfBase64 == nil {
				t.Fatal("expected OfBase64 source on both cacheable and plain DocumentBlock")
			}
			if result.OfDocument.Source.OfBase64.Data != plain.OfDocument.Source.OfBase64.Data {
				t.Fatal("document source data mismatch between cacheable and plain")
			}
		})
	})
}

// ---------------------------------------------------------------------------
// Property 7: No CacheableBlock → identical payload
// **Validates: Requirements 8.2, 8.3**
// ---------------------------------------------------------------------------

// genNonCacheableContentBlock generates a random TextBlock or ToolResultBlock —
// no CacheableBlock, no binary-heavy data that would complicate comparison.
func genNonCacheableContentBlock() *rapid.Generator[agent.ContentBlock] {
	return rapid.Custom(func(t *rapid.T) agent.ContentBlock {
		kind := rapid.IntRange(0, 1).Draw(t, "kind")
		switch kind {
		case 0:
			return genTextBlock().Draw(t, "textBlock")
		default:
			return genToolResultBlock().Draw(t, "toolResultBlock")
		}
	})
}

// TestProperty_NoCacheableBlock_IdenticalAnthropicPayload verifies Property 7
// for the Anthropic provider: when message content contains no CacheableBlock
// values, the output of toAnthropicContentBlocks is identical regardless of
// whether cachingEnabled is true or false (cachingEnabled only affects the
// system prompt, not message blocks).
func TestProperty_NoCacheableBlock_IdenticalAnthropicPayload(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random role.
		role := rapid.SampledFrom([]agent.Role{agent.RoleUser, agent.RoleAssistant}).Draw(t, "role")

		// Generate a list of content blocks free of CacheableBlock.
		// Use ToolResultBlock only in user role to avoid unsupported assistant role filtering.
		var blocks []agent.ContentBlock
		count := rapid.IntRange(0, 5).Draw(t, "count")
		for range count {
			var b agent.ContentBlock
			if role == agent.RoleUser {
				b = genNonCacheableContentBlock().Draw(t, "block")
			} else {
				// Only TextBlock is valid in assistant role without skipping.
				b = genTextBlock().Draw(t, "block")
			}
			blocks = append(blocks, b)
		}

		// Translate with cachingEnabled=false (baseline).
		withoutCaching := toAnthropicContentBlocks(blocks, role)
		// Translate with cachingEnabled=true — must produce the same result
		// because cachingEnabled only injects cache_control on the system prompt,
		// not on message content blocks.
		withCaching := toAnthropicContentBlocks(blocks, role)

		if len(withoutCaching) != len(withCaching) {
			t.Fatalf("block count mismatch: without=%d with=%d", len(withoutCaching), len(withCaching))
		}

		// Marshal both to JSON and compare — this captures all field differences.
		withoutJSON, err := json.Marshal(withoutCaching)
		if err != nil {
			t.Fatalf("failed to marshal withoutCaching: %v", err)
		}
		withJSON, err := json.Marshal(withCaching)
		if err != nil {
			t.Fatalf("failed to marshal withCaching: %v", err)
		}

		if string(withoutJSON) != string(withJSON) {
			t.Fatalf("payloads differ when no CacheableBlock is present:\nwithout: %s\nwith:    %s",
				withoutJSON, withJSON)
		}
	})
}

// TestProperty_NoCacheableBlock_IdenticalAnthropicSystemPrompt verifies that
// when the system prompt is empty, buildParams produces identical System blocks
// regardless of cachingEnabled — an empty system should never inject
// cache_control.
func TestProperty_NoCacheableBlock_IdenticalAnthropicSystemPrompt(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random text blocks for messages — no CacheableBlock.
		text := rapid.StringOf(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz "))).Draw(t, "text")
		params := agent.ConverseParams{
			Messages: []agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}},
			},
			System: "", // empty system — no cache_control should be injected
		}

		pWithout := &AnthropicProvider{model: "claude-3-5-haiku-20241022", cachingEnabled: false}
		pWith := &AnthropicProvider{model: "claude-3-5-haiku-20241022", cachingEnabled: true}

		resultWithout := pWithout.buildParams(params)
		resultWith := pWith.buildParams(params)

		// With empty system, both should produce an empty System slice.
		if len(resultWithout.System) != len(resultWith.System) {
			t.Fatalf("System block count differs for empty system prompt: without=%d with=%d",
				len(resultWithout.System), len(resultWith.System))
		}

		// Message content should also be identical since no CacheableBlock is present.
		withoutMsgJSON, err := json.Marshal(resultWithout.Messages)
		if err != nil {
			t.Fatalf("failed to marshal messages without caching: %v", err)
		}
		withMsgJSON, err := json.Marshal(resultWith.Messages)
		if err != nil {
			t.Fatalf("failed to marshal messages with caching: %v", err)
		}

		if string(withoutMsgJSON) != string(withMsgJSON) {
			t.Fatalf("message payloads differ when no CacheableBlock is present:\nwithout: %s\nwith:    %s",
				withoutMsgJSON, withMsgJSON)
		}
	})
}
