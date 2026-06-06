package agent

import (
	"testing"

	"pgregory.net/rapid"
)

// Feature: prompt-caching-support, Property 6: TokenUsage.Total() excludes cache tokens

// TestProperty_TokenUsageTotalExcludesCacheTokens verifies that for any TokenUsage
// value with arbitrary InputTokens, OutputTokens, CacheReadTokens, and
// CacheWriteTokens, Total() returns exactly InputTokens + OutputTokens and never
// includes cache tokens in the sum.
//
// **Validates: Requirements 5.9, 8.1**
func TestProperty_TokenUsageTotalExcludesCacheTokens(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate arbitrary token counts including negative values to stress
		// the invariant across the full int range.
		inputTokens := rapid.Int().Draw(rt, "inputTokens")
		outputTokens := rapid.Int().Draw(rt, "outputTokens")
		cacheReadTokens := rapid.Int().Draw(rt, "cacheReadTokens")
		cacheWriteTokens := rapid.Int().Draw(rt, "cacheWriteTokens")

		u := TokenUsage{
			InputTokens:      inputTokens,
			OutputTokens:     outputTokens,
			CacheReadTokens:  cacheReadTokens,
			CacheWriteTokens: cacheWriteTokens,
		}

		got := u.Total()
		want := inputTokens + outputTokens

		if got != want {
			rt.Fatalf(
				"Total() = %d, want %d (InputTokens=%d, OutputTokens=%d, CacheReadTokens=%d, CacheWriteTokens=%d)",
				got, want, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens,
			)
		}
	})
}
