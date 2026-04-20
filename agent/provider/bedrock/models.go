package bedrock

import rag "github.com/camilbinas/gude-agents/agent/rag/bedrock"

// Documented in docs/providers/bedrock.md — update when adding or removing models.

// Anthropic Claude models (Global cross-region inference).
func GlobalClaudeHaiku4_5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-haiku-4-5-20251001-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func GlobalClaudeSonnet4_5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-sonnet-4-5-20250929-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func GlobalClaudeSonnet4_6(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-sonnet-4-6", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func GlobalClaudeOpus4_5(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-opus-4-5-20251101-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func GlobalClaudeOpus4_6(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-opus-4-6-v1", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func GlobalClaudeOpus4_7(opts ...Option) (*BedrockProvider, error) {
	return New("global.anthropic.claude-opus-4-7", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}

// Amazon Nova models (Global cross-region inference).
func GlobalNova2Lite(opts ...Option) (*BedrockProvider, error) {
	return New("global.amazon.nova-2-lite-v1:0", append([]Option{withThinkingStyle(thinkingStyleNova2)}, opts...)...)
}

// Anthropic Claude models (US cross-region inference).
func US_ClaudeHaiku4_5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-haiku-4-5-20251001-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func US_ClaudeSonnet4_5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-sonnet-4-5-20250929-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func US_ClaudeSonnet4_6(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-sonnet-4-6", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func US_ClaudeOpus4_5(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-opus-4-5-20251101-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func US_ClaudeOpus4_6(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-opus-4-6-v1", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func US_ClaudeOpus4_7(opts ...Option) (*BedrockProvider, error) {
	return New("us.anthropic.claude-opus-4-7", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}

// Anthropic Claude models (US cross-region inference).
func US_Nova2Lite(opts ...Option) (*BedrockProvider, error) {
	return New("us.amazon.nova-2-lite-v1:0", append([]Option{withThinkingStyle(thinkingStyleNova2)}, opts...)...)
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
	return New("eu.amazon.nova-2-lite-v1:0", append([]Option{withThinkingStyle(thinkingStyleNova2)}, opts...)...)
}

// Anthropic Claude models (EU cross-region inference).
func EU_ClaudeHaiku4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-haiku-4-5-20251001-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func EU_ClaudeSonnet4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-4-5-20250929-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func EU_ClaudeSonnet4_6(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-4-6", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func EU_ClaudeOpus4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-5-20251101-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func EU_ClaudeOpus4_6(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-6-v1", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func EU_ClaudeOpus4_7(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-7", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}

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

// Other models (on-demand).
func GLM4_7Flash(opts ...Option) (*BedrockProvider, error) {
	return New("zai.glm-4.7-flash", opts...)
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

// Tier aliases — provider-agnostic shortcuts for common use cases.
func Cheapest(opts ...Option) (*BedrockProvider, error) {
	return GlobalClaudeHaiku4_5(opts...)
}
func Standard(opts ...Option) (*BedrockProvider, error) {
	return GlobalClaudeSonnet4_6(opts...)
}
func Smartest(opts ...Option) (*BedrockProvider, error) {
	return GlobalClaudeOpus4_7(opts...)
}
