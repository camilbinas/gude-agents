package bedrock

// Anthropic Claude models (EU cross-region inference).
// Documented in docs/providers.md — update when adding or removing models.
func ClaudeHaiku4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-haiku-4-5-20251001-v1:0", opts...)
}
func ClaudeSonnet4(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-4-20250514-v1:0", opts...)
}
func ClaudeSonnet4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-4-5-20250929-v1:0", opts...)
}
func ClaudeSonnet4_6(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-sonnet-4-6", opts...)
}
func ClaudeOpus4(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-6-v1", opts...)
}
func ClaudeOpus4_5(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-5-20251101-v1:0", opts...)
}
func ClaudeOpus4_6(opts ...Option) (*BedrockProvider, error) {
	return New("eu.anthropic.claude-opus-4-6-v1", opts...)
}

// Amazon Nova models (EU cross-region inference).
func NovaMicro(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-micro-v1:0", opts...)
}
func NovaLite(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-lite-v1:0", opts...)
}
func Nova2Lite(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-2-lite-v1:0", opts...)
}
func NovaPro(opts ...Option) (*BedrockProvider, error) {
	return New("eu.amazon.nova-pro-v1:0", opts...)
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

// Amazon Titan Embeddings.
func TitanEmbedV2(opts ...EmbedderOption) (*BedrockEmbedder, error) {
	return NewBedrockEmbedder("amazon.titan-embed-text-v2:0", opts...)
}

// Cohere Embed v3 models.
func CohereEmbedEnglishV3(opts ...EmbedderOption) (*BedrockEmbedder, error) {
	return NewBedrockEmbedder("cohere.embed-english-v3", opts...)
}
func CohereEmbedMultilingualV3(opts ...EmbedderOption) (*BedrockEmbedder, error) {
	return NewBedrockEmbedder("cohere.embed-multilingual-v3", opts...)
}

// Cohere Embed v4 — multimodal (text + images). Requires cross-region inference.
// Use the EU geo profile (eu.cohere.embed-v4:0) from eu-central-1.
func CohereEmbedV4(opts ...EmbedderOption) (*BedrockEmbedder, error) {
	return NewBedrockEmbedder("eu.cohere.embed-v4:0", opts...)
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
