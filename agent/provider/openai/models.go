package openai

// GPT models.
// Documented in docs/providers.md — update when adding or removing models.
func GPT4o(opts ...Option) (*OpenAIProvider, error)      { return New("gpt-4o", opts...) }
func GPT4oMini(opts ...Option) (*OpenAIProvider, error)  { return New("gpt-4o-mini", opts...) }
func GPT4_1(opts ...Option) (*OpenAIProvider, error)     { return New("gpt-4.1", opts...) }
func GPT4_1Mini(opts ...Option) (*OpenAIProvider, error) { return New("gpt-4.1-mini", opts...) }
func GPT4_1Nano(opts ...Option) (*OpenAIProvider, error) { return New("gpt-4.1-nano", opts...) }
func GPT5(opts ...Option) (*OpenAIProvider, error)       { return New("gpt-5", opts...) }
func GPT5Mini(opts ...Option) (*OpenAIProvider, error)   { return New("gpt-5-mini", opts...) }
func GPT5Nano(opts ...Option) (*OpenAIProvider, error)   { return New("gpt-5-nano", opts...) }

// Reasoning models.
func O3(opts ...Option) (*OpenAIProvider, error)     { return New("o3", opts...) }
func O3Mini(opts ...Option) (*OpenAIProvider, error) { return New("o3-mini", opts...) }
func O4Mini(opts ...Option) (*OpenAIProvider, error) { return New("o4-mini", opts...) }

// Embedding models.
func EmbeddingSmall(opts ...EmbedderOption) (*OpenAIEmbedder, error) {
	return NewOpenAIEmbedder(append([]EmbedderOption{WithEmbedderModel("text-embedding-3-small")}, opts...)...)
}
func EmbeddingLarge(opts ...EmbedderOption) (*OpenAIEmbedder, error) {
	return NewOpenAIEmbedder(append([]EmbedderOption{WithEmbedderModel("text-embedding-3-large")}, opts...)...)
}

// Tier aliases — provider-agnostic shortcuts for common use cases.
func Cheapest(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5-nano", opts...) }
func Standard(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5-mini", opts...) }
func Smartest(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5", opts...) }
