// Package provider contains shared constants used across all provider implementations.
package provider

// DefaultMaxTokens is the default maximum number of tokens in a provider response.
const DefaultMaxTokens = 8192

// ThinkingEffort is a typed effort level for extended thinking / reasoning.
// Use the predefined constants — arbitrary string values are not supported and
// will either be rejected by the upstream API (for enum-style providers like
// OpenAI and Bedrock Nova 2) or silently disable thinking (for budget-style
// providers like Anthropic, Bedrock Claude, and Gemini, where unknown levels
// resolve to a zero budget).
//
// For direct control over the token budget on budget-style providers, use the
// provider-specific WithThinkingBudget option instead of WithThinking.
type ThinkingEffort string

// Predefined thinking effort levels. Not every provider supports every level:
//   - OpenAI: minimal, low, medium, high
//   - Bedrock Nova 2: low, medium, high
//   - Anthropic, Bedrock Claude, Gemini: all four (mapped to a token budget)
const (
	ThinkingMinimal ThinkingEffort = "minimal"
	ThinkingLow     ThinkingEffort = "low"
	ThinkingMedium  ThinkingEffort = "medium"
	ThinkingHigh    ThinkingEffort = "high"
)

// ThinkingBudgets maps effort levels to default token budgets for providers
// that take an integer budget (Anthropic, Bedrock Claude, Gemini). For finer
// control, use the provider-specific WithThinkingBudget option.
var ThinkingBudgets = map[ThinkingEffort]int64{
	ThinkingMinimal: 1024,
	ThinkingLow:     2048,
	ThinkingMedium:  8192,
	ThinkingHigh:    16384,
}
