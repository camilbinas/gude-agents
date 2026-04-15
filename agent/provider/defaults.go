// Package provider contains shared constants used across all provider implementations.
package provider

// DefaultMaxTokens is the default maximum number of tokens in a provider response.
const DefaultMaxTokens = 8192

// Thinking effort levels for WithThinking. Use these constants across all providers.
const (
	ThinkingLow    = "low"
	ThinkingMedium = "medium"
	ThinkingHigh   = "high"
)

// thinkingBudgets maps effort levels to token budgets for providers that need them
// (Anthropic, Bedrock Claude). Unexported — internal use only.
var ThinkingBudgets = map[string]int64{
	ThinkingLow:    2048,
	ThinkingMedium: 8192,
	ThinkingHigh:   16384,
}
