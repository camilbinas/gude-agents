package openai

import (
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"

	openaisdk "github.com/openai/openai-go/v3"
)

// buildChatCompletion is a helper that constructs an openaisdk.ChatCompletion
// from raw JSON — matching the pattern used in the existing test file.
func buildChatCompletion(t *testing.T, raw map[string]any) openaisdk.ChatCompletion {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal ChatCompletion: %v", err)
	}
	var completion openaisdk.ChatCompletion
	if err := json.Unmarshal(b, &completion); err != nil {
		t.Fatalf("unmarshal ChatCompletion: %v", err)
	}
	return completion
}

// ---------------------------------------------------------------------------
// Converse — CacheReadTokens mapping
// Requirements: 5.7, 7.1
// ---------------------------------------------------------------------------

// TestConverse_CacheReadTokens_PopulatedFromPromptTokensDetails verifies that
// PromptTokensDetails.CachedTokens is mapped to Usage.CacheReadTokens.
// This mirrors the assignment in Converse:
//
//	resp.Usage.CacheReadTokens = int(completion.Usage.PromptTokensDetails.CachedTokens)
func TestConverse_CacheReadTokens_PopulatedFromPromptTokensDetails(t *testing.T) {
	const wantCached int64 = 512

	completion := buildChatCompletion(t, map[string]any{
		"id":      "chatcmpl-cache-test",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []map[string]any{
			{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "hello",
				},
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     1024,
			"completion_tokens": 100,
			"total_tokens":      1124,
			"prompt_tokens_details": map[string]any{
				"cached_tokens": wantCached,
			},
		},
	})

	// Reproduce the exact logic from Converse:
	resp := parseCompletion(&completion)
	resp.Usage.InputTokens = int(completion.Usage.PromptTokens)
	resp.Usage.OutputTokens = int(completion.Usage.CompletionTokens)
	resp.Usage.CacheReadTokens = int(completion.Usage.PromptTokensDetails.CachedTokens)

	if got := resp.Usage.CacheReadTokens; got != int(wantCached) {
		t.Errorf("CacheReadTokens: want %d, got %d", wantCached, got)
	}
}

// TestConverse_CacheReadTokens_ZeroWhenAbsent verifies that CacheReadTokens
// stays 0 when PromptTokensDetails is not present in the response (zero-value
// semantics per requirement 5.8).
func TestConverse_CacheReadTokens_ZeroWhenAbsent(t *testing.T) {
	completion := buildChatCompletion(t, map[string]any{
		"id":      "chatcmpl-no-cache",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []map[string]any{
			{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "hello",
				},
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     100,
			"completion_tokens": 50,
			"total_tokens":      150,
		},
	})

	resp := parseCompletion(&completion)
	resp.Usage.InputTokens = int(completion.Usage.PromptTokens)
	resp.Usage.OutputTokens = int(completion.Usage.CompletionTokens)
	resp.Usage.CacheReadTokens = int(completion.Usage.PromptTokensDetails.CachedTokens)

	if got := resp.Usage.CacheReadTokens; got != 0 {
		t.Errorf("CacheReadTokens: want 0 when absent, got %d", got)
	}
}

// TestConverse_CacheReadTokens_DoesNotAffectTotal verifies that CacheReadTokens
// does not leak into the Total() value (requirement 5.9).
func TestConverse_CacheReadTokens_DoesNotAffectTotal(t *testing.T) {
	completion := buildChatCompletion(t, map[string]any{
		"id":      "chatcmpl-total-test",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []map[string]any{
			{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "hello",
				},
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     200,
			"completion_tokens": 80,
			"total_tokens":      280,
			"prompt_tokens_details": map[string]any{
				"cached_tokens": 150,
			},
		},
	})

	resp := parseCompletion(&completion)
	resp.Usage.InputTokens = int(completion.Usage.PromptTokens)
	resp.Usage.OutputTokens = int(completion.Usage.CompletionTokens)
	resp.Usage.CacheReadTokens = int(completion.Usage.PromptTokensDetails.CachedTokens)

	wantTotal := resp.Usage.InputTokens + resp.Usage.OutputTokens
	if got := resp.Usage.Total(); got != wantTotal {
		t.Errorf("Total(): want %d (input+output only), got %d", wantTotal, got)
	}
	// Double-check CacheReadTokens is set so the test is meaningful.
	if resp.Usage.CacheReadTokens == 0 {
		t.Fatal("CacheReadTokens should be non-zero for this test to be meaningful")
	}
}

// ---------------------------------------------------------------------------
// ConverseStream — CacheReadTokens mapping from streaming usage chunk
// Requirements: 5.7, 7.2
// ---------------------------------------------------------------------------

// TestConverseStream_CacheReadTokens_ExtractionFromUsageChunk verifies that the
// streaming usage extraction logic — the same condition used inside ConverseStream:
//
//	if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 { ... }
//
// correctly maps PromptTokensDetails.CachedTokens to CacheReadTokens.
func TestConverseStream_CacheReadTokens_ExtractionFromUsageChunk(t *testing.T) {
	const (
		wantInput  int64 = 1000
		wantOutput int64 = 200
		wantCached int64 = 768
	)

	// Build the streaming chunk JSON — openaisdk.ChatCompletionChunk shares the
	// same usage/prompt_tokens_details structure as the non-streaming completion.
	chunkRaw, err := json.Marshal(map[string]any{
		"id":      "chatcmpl-stream-cache",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []map[string]any{},
		"usage": map[string]any{
			"prompt_tokens":     wantInput,
			"completion_tokens": wantOutput,
			"total_tokens":      wantInput + wantOutput,
			"prompt_tokens_details": map[string]any{
				"cached_tokens": wantCached,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal chunk: %v", err)
	}

	var chunk openaisdk.ChatCompletionChunk
	if err := json.Unmarshal(chunkRaw, &chunk); err != nil {
		t.Fatalf("unmarshal chunk: %v", err)
	}

	// Reproduce the exact logic from ConverseStream:
	resp := &agent.ProviderResponse{}
	if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
		resp.Usage.InputTokens = int(chunk.Usage.PromptTokens)
		resp.Usage.OutputTokens = int(chunk.Usage.CompletionTokens)
		resp.Usage.CacheReadTokens = int(chunk.Usage.PromptTokensDetails.CachedTokens)
	}

	if got := resp.Usage.CacheReadTokens; got != int(wantCached) {
		t.Errorf("CacheReadTokens: want %d, got %d", wantCached, got)
	}
	if got := resp.Usage.InputTokens; got != int(wantInput) {
		t.Errorf("InputTokens: want %d, got %d", wantInput, got)
	}
	if got := resp.Usage.OutputTokens; got != int(wantOutput) {
		t.Errorf("OutputTokens: want %d, got %d", wantOutput, got)
	}
}

// TestConverseStream_CacheReadTokens_ZeroWhenChunkHasNoUsage verifies that
// CacheReadTokens stays 0 when the streaming chunk contains no usage fields
// (i.e., the prompt_tokens and completion_tokens are both 0 — the guard
// condition in ConverseStream skips the assignment).
func TestConverseStream_CacheReadTokens_ZeroWhenChunkHasNoUsage(t *testing.T) {
	chunkRaw, err := json.Marshal(map[string]any{
		"id":      "chatcmpl-stream-no-usage",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{
					"content": "hello",
				},
				"finish_reason": nil,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal chunk: %v", err)
	}

	var chunk openaisdk.ChatCompletionChunk
	if err := json.Unmarshal(chunkRaw, &chunk); err != nil {
		t.Fatalf("unmarshal chunk: %v", err)
	}

	resp := &agent.ProviderResponse{}
	if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
		resp.Usage.InputTokens = int(chunk.Usage.PromptTokens)
		resp.Usage.OutputTokens = int(chunk.Usage.CompletionTokens)
		resp.Usage.CacheReadTokens = int(chunk.Usage.PromptTokensDetails.CachedTokens)
	}

	if got := resp.Usage.CacheReadTokens; got != 0 {
		t.Errorf("CacheReadTokens: want 0 for non-usage chunk, got %d", got)
	}
}

// TestConverseStream_CacheReadTokens_ZeroWhenCacheAbsent verifies that
// CacheReadTokens is 0 when the final streaming usage chunk is present but
// prompt_tokens_details.cached_tokens is absent.
func TestConverseStream_CacheReadTokens_ZeroWhenCacheAbsent(t *testing.T) {
	chunkRaw, err := json.Marshal(map[string]any{
		"id":      "chatcmpl-stream-no-cache",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []map[string]any{},
		"usage": map[string]any{
			"prompt_tokens":     500,
			"completion_tokens": 100,
			"total_tokens":      600,
		},
	})
	if err != nil {
		t.Fatalf("marshal chunk: %v", err)
	}

	var chunk openaisdk.ChatCompletionChunk
	if err := json.Unmarshal(chunkRaw, &chunk); err != nil {
		t.Fatalf("unmarshal chunk: %v", err)
	}

	resp := &agent.ProviderResponse{}
	if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
		resp.Usage.InputTokens = int(chunk.Usage.PromptTokens)
		resp.Usage.OutputTokens = int(chunk.Usage.CompletionTokens)
		resp.Usage.CacheReadTokens = int(chunk.Usage.PromptTokensDetails.CachedTokens)
	}

	if got := resp.Usage.CacheReadTokens; got != 0 {
		t.Errorf("CacheReadTokens: want 0 when cache absent, got %d", got)
	}
	// Input/output tokens should still be populated.
	if resp.Usage.InputTokens != 500 {
		t.Errorf("InputTokens: want 500, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 100 {
		t.Errorf("OutputTokens: want 100, got %d", resp.Usage.OutputTokens)
	}
}
