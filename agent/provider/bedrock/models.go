package bedrock

import (
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	rag "github.com/camilbinas/gude-agents/agent/rag/bedrock"
)

// modelProfile is what Gude knows about one Bedrock model, independent of how
// the caller constructs the provider.
type modelProfile struct {
	capabilities agent.ModelCapabilities

	// thinkingStyle is the request shape Bedrock needs for reasoning on this
	// model. thinkingStyleNone means Gude sends no reasoning configuration.
	thinkingStyle thinkingStyle
}

// catalog is the single source of truth for known Bedrock models. New consults
// it before applying caller options, so New("global.anthropic.claude-sonnet-5-5")
// and GlobalClaudeSonnet5_5() are identical.
//
// Entries are keyed by the model ID without its inference-profile prefix
// (global., us., eu., au., jp., ...): the same model has the same limits and
// request shape whichever profile routes to it. See lookupProfile.
//
// Entries hold only facts documented in the Amazon Bedrock model cards
// (https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-*.html) and
// supported by this adapter. A field that is not listed is intentionally
// Unknown. Numeric limits are advisory metadata, not enforced limits; where a
// card rounds a limit ("128K") the catalog uses the round decimal value, which
// never overstates it.
//
// Deliberately not listed, because no value was verified: every other model
// constructor in this file (Nova 2 Lite aside, which is listed), including the
// Qwen, DeepSeek, Moonshot, Mistral, MiniMax, NVIDIA and Z.AI models. Those
// cards either round a limit up ("164K", "203K") or were not checked. They
// are still valid; they report the adapter baseline.
var catalog = map[string]modelProfile{
	// Anthropic Claude.
	"anthropic.claude-haiku-4-5-20251001-v1:0":  claude(200_000, 64_000),
	"anthropic.claude-sonnet-4-5-20250929-v1:0": claude(200_000, 64_000),
	"anthropic.claude-sonnet-4-6": {
		capabilities: agent.ModelCapabilities{
			ContextWindowTokens: 1_000_000,
			MaxOutputTokens:     64_000,
			ToolUse:             agent.Supported,
			ToolChoice:          agent.ToolChoiceCapabilities{Specific: agent.Supported},
		},
		thinkingStyle: thinkingStyleClaude,
	},
	"anthropic.claude-sonnet-5":               claude(1_000_000, 128_000),
	"anthropic.claude-opus-4-5-20251101-v1:0": claude(200_000, 64_000),
	"anthropic.claude-opus-4-6-v1":            claude(1_000_000, 128_000),
	"anthropic.claude-opus-4-7":               claude(1_000_000, 128_000),
	"anthropic.claude-opus-4-8":               claude(1_000_000, 128_000),
	"anthropic.claude-opus-5":                 claude(1_000_000, 128_000),
	"anthropic.claude-fable-5":                claude(1_000_000, 128_000),
	"anthropic.claude-opus-5-5":               claude55(),
	"anthropic.claude-sonnet-5-5":             claude55(),

	// Amazon Nova.
	"amazon.nova-micro-v1:0": plain(128_000, 10_000),
	"amazon.nova-lite-v1:0":  plain(300_000, 10_000),
	"amazon.nova-pro-v1:0":   plain(300_000, 10_000),
	"amazon.nova-2-lite-v1:0": {
		capabilities:  agent.ModelCapabilities{ContextWindowTokens: 1_000_000, MaxOutputTokens: 64_000},
		thinkingStyle: thinkingStyleNova2,
	},

	// OpenAI models on Bedrock.
	"openai.gpt-oss-20b-1:0":  plain(128_000, 16_000),
	"openai.gpt-oss-120b-1:0": plain(128_000, 16_000),
	"openai.gpt-6-astra":      plain(1_050_000, 128_000),
	"openai.gpt-6-luna":       plain(1_050_000, 128_000),
	"openai.gpt-6.1-sol":      plain(1_000_000, 131_072),

	// Qwen on Bedrock.
	"qwen.qwen3-235b-a22b-2507-v1:0": plain(256_000, 8_000),

	// xAI. The card documents the context window but no output ceiling.
	"xai.grok-4.6": {
		capabilities:  agent.ModelCapabilities{ContextWindowTokens: 500_000},
		thinkingStyle: thinkingStyleGrok,
	},
}

// claude returns the profile of a Claude model that uses Claude-style
// thinking and has only documented limits.
func claude(contextWindow, maxOutput int) modelProfile {
	return modelProfile{
		capabilities:  agent.ModelCapabilities{ContextWindowTokens: contextWindow, MaxOutputTokens: maxOutput},
		thinkingStyle: thinkingStyleClaude,
	}
}

// claude55 is the profile shared by the Claude 5.5 models: automatic tool
// choice only, because forced and named tool choice are not supported.
func claude55() modelProfile {
	return modelProfile{
		capabilities: agent.ModelCapabilities{
			ContextWindowTokens: 1_000_000,
			MaxOutputTokens:     128_000,
			ToolUse:             agent.Supported,
			ToolChoice: agent.ToolChoiceCapabilities{
				Auto:     agent.Supported,
				Required: agent.Unsupported,
				Specific: agent.Unsupported,
			},
		},
		thinkingStyle: thinkingStyleClaude,
	}
}

// plain returns a profile with documented limits and no reasoning request
// shape.
func plain(contextWindow, maxOutput int) modelProfile {
	return modelProfile{capabilities: agent.ModelCapabilities{ContextWindowTokens: contextWindow, MaxOutputTokens: maxOutput}}
}

// inferenceProfilePrefixes are the geography prefixes Bedrock puts in front of
// a model ID to select a cross-region inference profile.
var inferenceProfilePrefixes = map[string]bool{
	"global": true, "us": true, "eu": true, "apac": true,
	"au": true, "jp": true, "in": true, "ca": true, "us-gov": true,
}

// lookupProfile finds the catalog entry for a model ID, accepting the bare
// model ID or one prefixed by an inference-profile geography.
func lookupProfile(modelID string) (modelProfile, bool) {
	if profile, ok := catalog[modelID]; ok {
		return profile, true
	}
	if prefix, rest, found := strings.Cut(modelID, "."); found && inferenceProfilePrefixes[prefix] {
		profile, ok := catalog[rest]
		return profile, ok
	}
	return modelProfile{}, false
}

// Named Bedrock models. Each delegates to New with its canonical model ID; the
// catalog supplies the capability metadata and request shape. Choose a model
// and inference profile that is available in your AWS region and account.
// Anthropic Claude models (Global cross-region inference).
func GlobalClaudeHaiku4_5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-haiku-4-5-20251001-v1:0", opts...)
}
func GlobalClaudeSonnet4_5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-sonnet-4-5-20250929-v1:0", opts...)
}
func GlobalClaudeSonnet4_6(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-sonnet-4-6", opts...)
}
func GlobalClaudeSonnet5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-sonnet-5", opts...)
}
func GlobalClaudeOpus4_5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-opus-4-5-20251101-v1:0", opts...)
}
func GlobalClaudeOpus4_6(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-opus-4-6-v1", opts...)
}
func GlobalClaudeOpus4_7(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-opus-4-7", opts...)
}
func GlobalClaudeOpus4_8(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-opus-4-8", opts...)
}
func GlobalClaudeOpus5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-opus-5", opts...)
}
func GlobalClaudeFable5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-fable-5", opts...)
}
func GlobalClaudeOpus5_5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-opus-5-5", opts...)
}

// Claude Sonnet 5.5 is Global-only on Bedrock — no us./eu. inference profile
// exists for this model as of this writing.
func GlobalClaudeSonnet5_5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-sonnet-5-5", opts...)
}

// Amazon Nova models (Global cross-region inference).
func GlobalNova2Lite(opts ...Option) (*BedrockProvider, error) {
	return New("global.amazon.nova-2-lite-v1:0", opts...)
}

// Anthropic Claude models (US cross-region inference).
func US_ClaudeHaiku4_5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-haiku-4-5-20251001-v1:0", opts...)
}
func US_ClaudeSonnet4_5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-sonnet-4-5-20250929-v1:0", opts...)
}
func US_ClaudeSonnet4_6(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-sonnet-4-6", opts...)
}
func US_ClaudeSonnet5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-sonnet-5", opts...)
}
func US_ClaudeOpus4_5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-opus-4-5-20251101-v1:0", opts...)
}
func US_ClaudeOpus4_6(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-opus-4-6-v1", opts...)
}
func US_ClaudeOpus4_7(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-opus-4-7", opts...)
}
func US_ClaudeOpus4_8(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-opus-4-8", opts...)
}
func US_ClaudeOpus5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-opus-5", opts...)
}
func US_ClaudeFable5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-fable-5", opts...)
}
func US_ClaudeOpus5_5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-opus-5-5", opts...)
}

// Claude Sonnet 5.5 has no US geo inference profile on Bedrock — only Global.
// Use GlobalClaudeSonnet5_5 instead.

// Anthropic Claude models (US cross-region inference).
func US_Nova2Lite(opts ...Option) (*BedrockProvider, error) {
	return New("us.amazon.nova-2-lite-v1:0", opts...)
}

// Amazon Nova models (EU cross-region inference).
func EU_NovaMicro(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-micro-v1:0", opts...)
}
func EU_NovaLite(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-lite-v1:0", opts...)
}
func EU_NovaPro(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-pro-v1:0", opts...)
}
func EU_Nova2Lite(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-2-lite-v1:0", opts...)
}

// Anthropic Claude models (EU cross-region inference).
func EU_ClaudeHaiku4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-haiku-4-5-20251001-v1:0", opts...)
}
func EU_ClaudeSonnet4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-4-5-20250929-v1:0", opts...)
}
func EU_ClaudeSonnet4_6(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-4-6", opts...)
}
func EU_ClaudeSonnet5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-5", opts...)
}
func EU_ClaudeOpus4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-5-20251101-v1:0", opts...)
}
func EU_ClaudeOpus4_6(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-6-v1", opts...)
}
func EU_ClaudeOpus4_7(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-7", opts...)
}
func EU_ClaudeOpus4_8(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-8", opts...)
}
func EU_ClaudeOpus5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-5", opts...)
}
func EU_ClaudeOpus5_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-5-5", opts...)
}

// Claude Opus 5.5 also has AU and JP geo inference profiles on Bedrock, unlike
// any earlier Claude model in this file.
func AU_ClaudeOpus5_5(opts ...Option) (*BedrockProvider, error) {
	return New("au.anthropic.claude-opus-5-5", opts...)
}
func JP_ClaudeOpus5_5(opts ...Option) (*BedrockProvider, error) {
	return New("jp.anthropic.claude-opus-5-5", opts...)
}

// Fable 5 has no EU geo route.

// Claude Sonnet 5.5 has no US or EU geo inference profile on Bedrock — only
// Global. Use GlobalClaudeSonnet5_5 instead.

// Amazon Nova models (on-demand).
func NovaMicro(opts ...Option) (*BedrockProvider, error) {
	return New("amazon.nova-micro-v1:0", opts...)
}
func NovaLite(opts ...Option) (*BedrockProvider, error) {
	return New("amazon.nova-lite-v1:0", opts...)
}
func NovaPro(opts ...Option) (*BedrockProvider, error) {
	return New("amazon.nova-pro-v1:0", opts...)
}

// Qwen models (on-demand).
func Qwen3_235B(opts ...Option) (*BedrockProvider, error) {
	return New("qwen.qwen3-235b-a22b-2507-v1:0", opts...)
}
func Qwen3_32B(opts ...Option) (*BedrockProvider, error) {
	return New("qwen.qwen3-32b-v1:0", opts...)
}
func Qwen3Coder30B(opts ...Option) (*BedrockProvider, error) {
	return New("qwen.qwen3-coder-30b-a3b-v1:0", opts...)
}
func Qwen3CoderNext(opts ...Option) (*BedrockProvider, error) {
	return New("qwen.qwen3-coder-next", opts...)
}
func Qwen3Next80B(opts ...Option) (*BedrockProvider, error) {
	return New("qwen.qwen3-next-80b-a3b", opts...)
}

// DeepSeek models (on-demand).
func DeepSeekV3_2(opts ...Option) (*BedrockProvider, error) {
	return New("deepseek.v3.2", opts...)
}

// Moonshot AI models (on-demand).
func KimiK2_5(opts ...Option) (*BedrockProvider, error) {
	return New("moonshotai.kimi-k2.5", opts...)
}

// "moonshot." not "moonshotai." — differs from Kimi K2.5 upstream, not a typo.
func KimiK2Thinking(opts ...Option) (*BedrockProvider, error) {
	return New("moonshot.kimi-k2-thinking", opts...)
}

// Mistral AI models (on-demand).
func MistralLarge3(opts ...Option) (*BedrockProvider, error) {
	return New("mistral.mistral-large-3-675b-instruct", opts...)
}
func Devstral2_123B(opts ...Option) (*BedrockProvider, error) {
	return New("mistral.devstral-2-123b", opts...)
}

// MiniMax models (on-demand).
func MiniMaxM2_1(opts ...Option) (*BedrockProvider, error) {
	return New("minimax.minimax-m2.1", opts...)
}
func MiniMaxM2_5(opts ...Option) (*BedrockProvider, error) {
	return New("minimax.minimax-m2.5", opts...)
}

// OpenAI models on Bedrock (on-demand).
func GPT_OSS_20B(opts ...Option) (*BedrockProvider, error) {
	return New("openai.gpt-oss-20b-1:0", opts...)
}
func GPT_OSS_120B(opts ...Option) (*BedrockProvider, error) {
	return New("openai.gpt-oss-120b-1:0", opts...)
}

// Nvidia models (on-demand).
func NemotronNano2_9B(opts ...Option) (*BedrockProvider, error) {
	return New("nvidia.nemotron-nano-9b-v2", opts...)
}
func NemotronNano2_12B(opts ...Option) (*BedrockProvider, error) {
	return New("nvidia.nemotron-nano-12b-v2", opts...)
}
func NemotronNano3_35B(opts ...Option) (*BedrockProvider, error) {
	return New("nvidia.nemotron-nano-3-30b", opts...)
}
func NemotronSuper3_120B(opts ...Option) (*BedrockProvider, error) {
	return New("nvidia.nemotron-super-3-120b", opts...)
}

// Z.AI models (on-demand).
func GLM4_7Flash(opts ...Option) (*BedrockProvider, error) {
	return New("zai.glm-4.7-flash", opts...)
}
func GLM4_7(opts ...Option) (*BedrockProvider, error) {
	return New("zai.glm-4.7", opts...)
}
func GLM5(opts ...Option) (*BedrockProvider, error) {
	return New("zai.glm-5", opts...)
}

// OpenAI chat models on Bedrock (Global cross-region inference). These are
// distinct from the on-demand GPT-OSS models above — Astra, Sol, and Luna
// are served only through inference profiles, not on-demand throughput.
func GlobalGPT6Astra(opts ...Option) (*BedrockProvider, error) {
	return New("global.openai.gpt-6-astra", opts...)
}
func GlobalGPT6Luna(opts ...Option) (*BedrockProvider, error) {
	return New("global.openai.gpt-6-luna", opts...)
}

// GPT-6.1 Sol has no Global inference profile on Bedrock at launch — only US.
// Use US_GPT6_1Sol instead.

// OpenAI chat models on Bedrock (US cross-region inference).
func US_GPT6Astra(opts ...Option) (*BedrockProvider, error) {
	return New("us.openai.gpt-6-astra", opts...)
}
func US_GPT6_1Sol(opts ...Option) (*BedrockProvider, error) {
	return New("us.openai.gpt-6.1-sol", opts...)
}
func US_GPT6Luna(opts ...Option) (*BedrockProvider, error) {
	return New("us.openai.gpt-6-luna", opts...)
}

// xAI models on Bedrock (Global cross-region inference). Reasoning is always
// on; effort is configurable via WithThinking (low, medium, high, xhigh).
func GlobalGrok4_6(opts ...Option) (*BedrockProvider, error) {
	return New("global.xai.grok-4.6", opts...)
}

// xAI models on Bedrock (US cross-region inference). Grok 4.6 has no EU geo
// inference profile on Bedrock — only US and Global.
func US_Grok4_6(opts ...Option) (*BedrockProvider, error) {
	return New("us.xai.grok-4.6", opts...)
}

// MustEmbedder is a helper that wraps a (*rag.Embedder, error) call and panics on error.
// Forwards to agent/rag/bedrock.MustEmbedder.
func MustEmbedder(e *rag.Embedder, err error) *rag.Embedder {
	return rag.MustEmbedder(e, err)
}

// Amazon Titan and Cohere embedding models.
// These forward to agent/rag/bedrock — import that package directly for
// access to EmbedderOption and the Embedder type.
func TitanEmbedV2(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.TitanEmbedV2(opts...)
}
func CohereEmbedEnglishV3(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.CohereEmbedEnglishV3(opts...)
}
func CohereEmbedMultilingualV3(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.CohereEmbedMultilingualV3(opts...)
}
func CohereEmbedV4(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.CohereEmbedV4(opts...)
}

// MustReranker is a helper that wraps a (*rag.Reranker, error) call and panics on error.
// Forwards to agent/rag/bedrock.MustReranker.
func MustReranker(r *rag.Reranker, err error) *rag.Reranker {
	return rag.MustReranker(r, err)
}

// Cohere and Amazon reranker models.
// These forward to agent/rag/bedrock — import that package directly for
// access to RerankerOption and the Reranker type.
func CohereRerank35(opts ...rag.RerankerOption) (*rag.Reranker, error) {
	return rag.CohereRerank35(opts...)
}
func AmazonRerank10(opts ...rag.RerankerOption) (*rag.Reranker, error) {
	return rag.AmazonRerank10(opts...)
}
