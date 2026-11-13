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
// limits. Tool-choice modes (auto/required/named) are not documented per model
// on the model pages, so they stay Unknown.
//
// Deliberately partial entries:
//   - gpt-6-sol, gpt-6-luna: tool use is left Unknown. The model pages state
//     that Chat Completions supports function calling only with
//     reasoning_effort set to none, which is not this adapter's default.
//   - gpt-6.1-sol, gpt-6.1-terra: their pages state that tool calling requires
//     the Responses API, so tool use is Unsupported through this adapter.
//
// Shut-down IDs (gpt-4.1-nano, o3-mini, o4-mini) are intentionally absent: the
// API no longer accepts them, so their named constructors fall back to the
// adapter baseline. Their Deprecated constructors remain for source
// compatibility.
// Native structured output (response_format json_schema) is documented on Chat
// Completions for the same models that support function calling there; the
// catalog marks it Supported exactly where ToolUse is Supported. Models whose
// tool calling is Unknown or Responses-API-only leave it Unknown/Unsupported
// so unknown models keep the tool path.
var catalog = map[string]modelProfile{
	"gpt-4o":      {capabilities: caps(128_000, 16_384, agent.Supported, agent.Supported)},
	"gpt-4o-mini": {capabilities: caps(128_000, 16_384, agent.Supported, agent.Supported)},

	"gpt-4.1":      {capabilities: caps(1_047_576, 32_768, agent.Supported, agent.Supported)},
	"gpt-4.1-mini": {capabilities: caps(1_047_576, 32_768, agent.Supported, agent.Supported)},

	"gpt-5":      {capabilities: caps(400_000, 128_000, agent.Supported, agent.Supported)},
	"gpt-5-mini": {capabilities: caps(400_000, 128_000, agent.Supported, agent.Supported)},
	"gpt-5-nano": {capabilities: caps(400_000, 128_000, agent.Supported, agent.Supported)},

	"gpt-5.4":      {capabilities: caps(1_050_000, 128_000, agent.Supported, agent.Supported)},
	"gpt-5.4-mini": {capabilities: caps(400_000, 128_000, agent.Supported, agent.Supported)},
	"gpt-5.4-nano": {capabilities: caps(400_000, 128_000, agent.Supported, agent.Supported)},
	"gpt-5.5":      {capabilities: caps(1_050_000, 128_000, agent.Supported, agent.Supported)},

	// "gpt-5.6" is an alias that OpenAI routes to GPT-5.6 Sol.
	"gpt-5.6":       {capabilities: caps(1_050_000, 128_000, agent.Supported, agent.Supported)},
	"gpt-5.6-sol":   {capabilities: caps(1_050_000, 128_000, agent.Supported, agent.Supported)},
	"gpt-5.6-terra": {capabilities: caps(1_050_000, 128_000, agent.Supported, agent.Supported)},
	"gpt-5.6-luna":  {capabilities: caps(1_050_000, 128_000, agent.Supported, agent.Supported)},

	"gpt-6-astra": {capabilities: caps(1_050_000, 128_000, agent.Supported, agent.Supported)},
	// gpt-6-sol/luna: tool calling (and so response_format) on Chat Completions
	// needs reasoning_effort=none, not this adapter's default; leave Unknown.
	"gpt-6-sol":  {capabilities: caps(1_050_000, 128_000, agent.Unknown, agent.Unknown)},
	"gpt-6-luna": {capabilities: caps(1_050_000, 128_000, agent.Unknown, agent.Unknown)},
	// gpt-6.1-sol/terra: tool calling requires the Responses API, so native
	// structured output is unavailable through this Chat Completions adapter.
	"gpt-6.1-sol":   {capabilities: caps(1_050_000, 128_000, agent.Unsupported, agent.Unsupported)},
	"gpt-6.1-terra": {capabilities: caps(1_050_000, 128_000, agent.Unsupported, agent.Unsupported)},

	"o3": {capabilities: caps(200_000, 100_000, agent.Supported, agent.Supported)},
}

func caps(contextWindow, maxOutput int, toolUse, nativeStructured agent.Capability) agent.ModelCapabilities {
	return agent.ModelCapabilities{
		ContextWindowTokens:    contextWindow,
		MaxOutputTokens:        maxOutput,
		ToolUse:                toolUse,
		NativeStructuredOutput: nativeStructured,
	}
}

// Named OpenAI models. Each delegates to New with its canonical model ID; the
// catalog supplies the capability metadata.

// Deprecated: OpenAI retires gpt-4o on 2027-01-14; use GPT6Luna.
func GPT4o(opts ...Option) (*OpenAIProvider, error) { return New("gpt-4o", opts...) }

// Deprecated: OpenAI retires gpt-4o-mini on 2027-01-14; use GPT6Luna.
func GPT4oMini(opts ...Option) (*OpenAIProvider, error) { return New("gpt-4o-mini", opts...) }

// Deprecated: OpenAI retires gpt-4.1 on 2027-01-14; use GPT6Luna.
func GPT4_1(opts ...Option) (*OpenAIProvider, error) { return New("gpt-4.1", opts...) }

// Deprecated: OpenAI retires gpt-4.1-mini on 2027-01-14; use GPT6Luna.
func GPT4_1Mini(opts ...Option) (*OpenAIProvider, error) { return New("gpt-4.1-mini", opts...) }

// Deprecated: OpenAI shut down gpt-4.1-nano on 2026-10-23; use GPT5_6Luna.
func GPT4_1Nano(opts ...Option) (*OpenAIProvider, error) { return New("gpt-4.1-nano", opts...) }

// Deprecated: OpenAI retires gpt-5 on 2026-12-11; use GPT6_1Sol.
func GPT5(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5", opts...) }

// Deprecated: OpenAI retires gpt-5-mini on 2026-12-11; use GPT6_1Terra.
func GPT5Mini(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5-mini", opts...) }

// Deprecated: OpenAI retires gpt-5-nano on 2026-12-11; use GPT6Luna.
func GPT5Nano(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5-nano", opts...) }

func GPT5_4(opts ...Option) (*OpenAIProvider, error)     { return New("gpt-5.4", opts...) }
func GPT5_4Mini(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.4-mini", opts...) }

// Deprecated: OpenAI retires gpt-5.4-nano on 2027-04-01; use GPT6Luna.
func GPT5_4Nano(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.4-nano", opts...) }

func GPT5_5(opts ...Option) (*OpenAIProvider, error)    { return New("gpt-5.5", opts...) }
func GPT5_6(opts ...Option) (*OpenAIProvider, error)    { return New("gpt-5.6", opts...) }
func GPT5_6Sol(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.6-sol", opts...) }
func GPT5_6Terra(opts ...Option) (*OpenAIProvider, error) {
	return New("gpt-5.6-terra", opts...)
}
func GPT5_6Luna(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.6-luna", opts...) }
func GPT6Astra(opts ...Option) (*OpenAIProvider, error)  { return New("gpt-6-astra", opts...) }
func GPT6Sol(opts ...Option) (*OpenAIProvider, error)    { return New("gpt-6-sol", opts...) }
func GPT6_1Sol(opts ...Option) (*OpenAIProvider, error)  { return New("gpt-6.1-sol", opts...) }
func GPT6Luna(opts ...Option) (*OpenAIProvider, error)   { return New("gpt-6-luna", opts...) }

// Omitted: gpt-5.5-pro, gpt-5.4-pro and gpt-5.3-codex are Responses API-only and
// 404 on Chat Completions. gpt-6.1-terra is in the catalog without a named
// constructor; use New("gpt-6.1-terra").

// Reasoning models.

// Deprecated: OpenAI retires o3 on 2026-12-11; use GPT6_1Sol.
func O3(opts ...Option) (*OpenAIProvider, error) { return New("o3", opts...) }

// Deprecated: OpenAI shut down o3-mini on 2026-10-23; use GPT5_6Sol.
func O3Mini(opts ...Option) (*OpenAIProvider, error) { return New("o3-mini", opts...) }

// Deprecated: OpenAI shut down o4-mini on 2026-10-23; use GPT5_6Terra.
func O4Mini(opts ...Option) (*OpenAIProvider, error) { return New("o4-mini", opts...) }

// Embedding models — forward to agent/rag/openai.
func EmbeddingSmall(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.EmbeddingSmall(opts...)
}
func EmbeddingLarge(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.EmbeddingLarge(opts...)
}
