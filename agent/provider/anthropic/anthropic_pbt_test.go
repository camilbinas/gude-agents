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
// Property 5: Anthropic DocumentBlock auto-caching
// **Validates: Requirements 3.1, 3.2**
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

// genImageBlock generates a fixed-data ImageBlock.
func genImageBlock() *rapid.Generator[agent.ImageBlock] {
	return rapid.Custom(func(t *rapid.T) agent.ImageBlock {
		_ = rapid.Bool().Draw(t, "imageVariant")

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

// genDocumentBlock generates a fixed-data DocumentBlock.
func genDocumentBlock() *rapid.Generator[agent.DocumentBlock] {
	return rapid.Custom(func(t *rapid.T) agent.DocumentBlock {
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

// TestProperty_DocumentBlockGetsCacheControlWhenCachingEnabled verifies that
// when cachingEnabled=true, every DocumentBlock in user messages gets
// cache_control set. When cachingEnabled=false, it is not set.
func TestProperty_DocumentBlockGetsCacheControlWhenCachingEnabled(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		inner := genDocumentBlock().Draw(t, "inner")
		blocks := []agent.ContentBlock{inner}

		// With caching disabled — no cache_control.
		withoutCaching := toAnthropicContentBlocks(blocks, agent.RoleUser, false)
		if len(withoutCaching) == 0 {
			t.Fatal("expected non-empty result without caching")
		}
		if withoutCaching[0].OfDocument == nil {
			t.Fatal("expected OfDocument for DocumentBlock")
		}
		if !param.IsOmitted(withoutCaching[0].OfDocument.CacheControl) {
			t.Fatal("expected CacheControl to be absent when caching is disabled")
		}

		// With caching enabled — cache_control must be set.
		withCaching := toAnthropicContentBlocks(blocks, agent.RoleUser, true)
		if len(withCaching) == 0 {
			t.Fatal("expected non-empty result with caching")
		}
		if withCaching[0].OfDocument == nil {
			t.Fatal("expected OfDocument for DocumentBlock")
		}
		if param.IsOmitted(withCaching[0].OfDocument.CacheControl) {
			t.Fatal("expected CacheControl to be set on DocumentBlock when caching is enabled")
		}
	})
}

// ---------------------------------------------------------------------------
// Property 7: No DocumentBlock → identical payload regardless of caching flag
// **Validates: Requirements 8.2, 8.3**
// ---------------------------------------------------------------------------

// genNonCacheableContentBlock generates a random TextBlock or ToolResultBlock —
// no DocumentBlock (which would be affected by cachingEnabled).
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

// TestProperty_NoDocumentBlock_IdenticalAnthropicPayload verifies that when
// message content contains no DocumentBlock values, the output of
// toAnthropicContentBlocks is identical regardless of cachingEnabled.
func TestProperty_NoDocumentBlock_IdenticalAnthropicPayload(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random role.
		role := rapid.SampledFrom([]agent.Role{agent.RoleUser, agent.RoleAssistant}).Draw(t, "role")

		// Generate a list of content blocks free of DocumentBlock.
		var blocks []agent.ContentBlock
		count := rapid.IntRange(0, 5).Draw(t, "count")
		for range count {
			var b agent.ContentBlock
			if role == agent.RoleUser {
				b = genNonCacheableContentBlock().Draw(t, "block")
			} else {
				b = genTextBlock().Draw(t, "block")
			}
			blocks = append(blocks, b)
		}

		// Translate with cachingEnabled=false (baseline).
		withoutCaching := toAnthropicContentBlocks(blocks, role, false)
		// Translate with cachingEnabled=true — must produce the same result
		// because cachingEnabled only affects DocumentBlocks and system prompt.
		withCaching := toAnthropicContentBlocks(blocks, role, true)

		if len(withoutCaching) != len(withCaching) {
			t.Fatalf("block count mismatch: without=%d with=%d", len(withoutCaching), len(withCaching))
		}

		// Marshal both to JSON and compare.
		withoutJSON, err := json.Marshal(withoutCaching)
		if err != nil {
			t.Fatalf("failed to marshal withoutCaching: %v", err)
		}
		withJSON, err := json.Marshal(withCaching)
		if err != nil {
			t.Fatalf("failed to marshal withCaching: %v", err)
		}

		if string(withoutJSON) != string(withJSON) {
			t.Fatalf("payloads differ when no DocumentBlock is present:\nwithout: %s\nwith:    %s",
				withoutJSON, withJSON)
		}
	})
}

// TestProperty_NoDocumentBlock_IdenticalAnthropicSystemPrompt verifies that
// when the system prompt is empty, buildParams produces identical System blocks
// regardless of cachingEnabled.
func TestProperty_NoDocumentBlock_IdenticalAnthropicSystemPrompt(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
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

		// Message content should also be identical since no DocumentBlock is present.
		withoutMsgJSON, err := json.Marshal(resultWithout.Messages)
		if err != nil {
			t.Fatalf("failed to marshal messages without caching: %v", err)
		}
		withMsgJSON, err := json.Marshal(resultWith.Messages)
		if err != nil {
			t.Fatalf("failed to marshal messages with caching: %v", err)
		}

		if string(withoutMsgJSON) != string(withMsgJSON) {
			t.Fatalf("message payloads differ when no DocumentBlock is present:\nwithout: %s\nwith:    %s",
				withoutMsgJSON, withMsgJSON)
		}
	})
}
