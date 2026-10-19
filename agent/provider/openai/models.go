package openai

import (
	"github.com/camilbinas/gude-agents/agent"
	rag "github.com/camilbinas/gude-agents/agent/rag/openai"
)

// modelProfile is what Gude knows about one model ID, independent of how the
// caller constructs the provider.
type modelProfile struct {
	capabilities agent.ModelCapabilities
}

// catalog is the single source of truth for known OpenAI model IDs. New
// consults it before applying caller options, so New("gpt-5.6-sol") and
// GPT5_6Sol() are identical.
//
// Entries hold only facts documented at
// https://developers.openai.com/api/docs/models/<model> and supported by this
// adapter, which talks to the Chat Completions API. A field that is not listed
// is intentionally Unknown. Numeric limits are advisory metadata, not enforced
// limits. Tool-choice modes are not documented per model, so they stay Unknown.
//
// Deliberately partial entries:
//   - gpt-6-sol, gpt-6-luna: tool use is left Unknown. The model pages state
//     that Chat Completions supports function calling only with
//     reasoning_effort set to none, which is not this adapter's default.
//   - gpt-6.1-sol: its page states that Chat Completions is supported without
//     tool calling, so tool use is Unsupported through this adapter. Context
//     and output limits are not published on the page and stay Unknown.
var catalog = map[string]modelProfile{
	"gpt-4o":      {capabilities: caps(128_000, 16_384, agent.Supported)},
	"gpt-4o-mini": {capabilities: caps(128_000, 16_384, agent.Supported)},

	"gpt-4.1":      {capabilities: caps(1_047_576, 32_768, agent.Supported)},
	"gpt-4.1-mini": {capabilities: caps(1_047_576, 32_768, agent.Supported)},
	"gpt-4.1-nano": {capabilities: caps(1_047_576, 32_768, agent.Supported)},

	"gpt-5":      {capabilities: caps(400_000, 128_000, agent.Supported)},
	"gpt-5-mini": {capabilities: caps(400_000, 128_000, agent.Supported)},
	"gpt-5-nano": {capabilities: caps(400_000, 128_000, agent.Supported)},

	"gpt-5.4":      {capabilities: caps(1_050_000, 128_000, agent.Supported)},
	"gpt-5.4-mini": {capabilities: caps(400_000, 128_000, agent.Supported)},
	"gpt-5.4-nano": {capabilities: caps(400_000, 128_000, agent.Supported)},
	"gpt-5.5":      {capabilities: caps(1_050_000, 128_000, agent.Supported)},

	// "gpt-5.6" is an alias that OpenAI routes to GPT-5.6 Sol.
	"gpt-5.6":       {capabilities: caps(1_050_000, 128_000, agent.Supported)},
	"gpt-5.6-sol":   {capabilities: caps(1_050_000, 128_000, agent.Supported)},
	"gpt-5.6-terra": {capabilities: caps(1_050_000, 128_000, agent.Supported)},
	"gpt-5.6-luna":  {capabilities: caps(1_050_000, 128_000, agent.Supported)},

	"gpt-6-astra": {capabilities: caps(1_050_000, 128_000, agent.Supported)},
	"gpt-6-sol":   {capabilities: caps(1_050_000, 128_000, agent.Unknown)},
	"gpt-6-luna":  {capabilities: caps(1_050_000, 128_000, agent.Unknown)},
	"gpt-6.1-sol": {capabilities: caps(0, 0, agent.Unsupported)},

	"o3":      {capabilities: caps(200_000, 100_000, agent.Supported)},
	"o3-mini": {capabilities: caps(200_000, 100_000, agent.Supported)},
	"o4-mini": {capabilities: caps(200_000, 100_000, agent.Supported)},
}

func caps(contextWindow, maxOutput int, toolUse agent.Capability) agent.ModelCapabilities {
	return agent.ModelCapabilities{
		ContextWindowTokens: contextWindow,
		MaxOutputTokens:     maxOutput,
		ToolUse:             toolUse,
	}
}

// Named OpenAI models. Each delegates to New with its canonical model ID; the
// catalog supplies the capability metadata.
func GPT4o(opts ...Option) (*OpenAIProvider, error)      { return New("gpt-4o", opts...) }
func GPT4oMini(opts ...Option) (*OpenAIProvider, error)  { return New("gpt-4o-mini", opts...) }
func GPT4_1(opts ...Option) (*OpenAIProvider, error)     { return New("gpt-4.1", opts...) }
func GPT4_1Mini(opts ...Option) (*OpenAIProvider, error) { return New("gpt-4.1-mini", opts...) }
func GPT4_1Nano(opts ...Option) (*OpenAIProvider, error) { return New("gpt-4.1-nano", opts...) }
func GPT5(opts ...Option) (*OpenAIProvider, error)       { return New("gpt-5", opts...) }
func GPT5Mini(opts ...Option) (*OpenAIProvider, error)   { return New("gpt-5-mini", opts...) }
func GPT5Nano(opts ...Option) (*OpenAIProvider, error)   { return New("gpt-5-nano", opts...) }
func GPT5_4(opts ...Option) (*OpenAIProvider, error)     { return New("gpt-5.4", opts...) }
func GPT5_4Mini(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.4-mini", opts...) }
func GPT5_4Nano(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.4-nano", opts...) }
func GPT5_5(opts ...Option) (*OpenAIProvider, error)     { return New("gpt-5.5", opts...) }
func GPT5_6(opts ...Option) (*OpenAIProvider, error)     { return New("gpt-5.6", opts...) }
func GPT5_6Sol(opts ...Option) (*OpenAIProvider, error)  { return New("gpt-5.6-sol", opts...) }
func GPT5_6Terra(opts ...Option) (*OpenAIProvider, error) {
	return New("gpt-5.6-terra", opts...)
}
func GPT5_6Luna(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.6-luna", opts...) }
func GPT6Astra(opts ...Option) (*OpenAIProvider, error)  { return New("gpt-6-astra", opts...) }
func GPT6Sol(opts ...Option) (*OpenAIProvider, error)    { return New("gpt-6-sol", opts...) }
func GPT6_1Sol(opts ...Option) (*OpenAIProvider, error)  { return New("gpt-6.1-sol", opts...) }
func GPT6Luna(opts ...Option) (*OpenAIProvider, error)   { return New("gpt-6-luna", opts...) }

// Omitted: gpt-5.5-pro, gpt-5.4-pro and gpt-5.3-codex are Responses API-only and
// 404 on Chat Completions.

// Reasoning models.
func O3(opts ...Option) (*OpenAIProvider, error)     { return New("o3", opts...) }
func O3Mini(opts ...Option) (*OpenAIProvider, error) { return New("o3-mini", opts...) }
func O4Mini(opts ...Option) (*OpenAIProvider, error) { return New("o4-mini", opts...) }

// Embedding models — forward to agent/rag/openai.
func EmbeddingSmall(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.EmbeddingSmall(opts...)
}
func EmbeddingLarge(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.EmbeddingLarge(opts...)
}
