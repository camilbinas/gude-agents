package bedrock

import rag "github.com/camilbinas/gude-agents/agent/rag/bedrock"

// Anthropic Claude models (EU cross-region inference).
// Documented in docs/providers.md — update when adding or removing models.
func ClaudeHaiku4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-haiku-4-5-20251001-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func ClaudeSonnet4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-4-5-20250929-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func ClaudeSonnet4_6(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-4-6", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func ClaudeOpus4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-5-20251101-v1:0", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func ClaudeOpus4_6(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-6-v1", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}
func ClaudeOpus4_7(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-7", append([]Option{withThinkingStyle(thinkingStyleClaude)}, opts...)...)
}

// Amazon Nova models (EU cross-region inference).
func NovaMicro(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-micro-v1:0", opts...)
}
func NovaLite(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-lite-v1:0", opts...)
}
func NovaPro(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-pro-v1:0", opts...)
}
func Nova2Lite(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-2-lite-v1:0", append([]Option{withThinkingStyle(thinkingStyleNova2)}, opts...)...)
}

// Qwen models (on-demand).
func Qwen3_235B(opts ...Option) (*BedrockProvider, error) {
	return New("qwen.qwen3-235b-a22b-2507-v1:0", opts...)
}
func Qwen3_32B(opts ...Option) (*BedrockProvider, error) { return New("qwen.qwen3-32b-v1:0", opts...) }
func Qwen3Coder30B(opts ...Option) (*BedrockProvider, error) {
	return New("qwen.qwen3-coder-30b-a3b-v1:0", opts...)
}

// MiniMax models (on-demand).
func MiniMaxM2_5(opts ...Option) (*BedrockProvider, error) {
	return New("minimax.minimax-m2.5", opts...)
}
func MiniMaxM2_1(opts ...Option) (*BedrockProvider, error) {
	return New("minimax.minimax-m2.1", opts...)
}

// OpenAI models on Bedrock (on-demand).
func GPT_OSS_120B(opts ...Option) (*BedrockProvider, error) {
	return New("openai.gpt-oss-120b-1:0", opts...)
}
func GPT_OSS_20B(opts ...Option) (*BedrockProvider, error) {
	return New("openai.gpt-oss-20b-1:0", opts...)
}

// Other models (on-demand).
func NemotronSuper120B(opts ...Option) (*BedrockProvider, error) {
	return New("nvidia.nemotron-super-3-120b", opts...)
}
func GLM4_7Flash(opts ...Option) (*BedrockProvider, error) { return New("zai.glm-4.7-flash", opts...) }

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
// All three map to the Amazon Nova family.
func Cheapest(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-micro-v1:0", opts...)
}
func Standard(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-pro-v1:0", opts...)
}
func Smartest(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-2-lite-v1:0", opts...)
}
