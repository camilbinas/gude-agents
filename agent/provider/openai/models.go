package openai

import rag "github.com/camilbinas/gude-agents/agent/rag/openai"

// GPT models.
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

func GPT5_6(opts ...Option) (*OpenAIProvider, error)      { return New("gpt-5.6", opts...) }
func GPT5_6Sol(opts ...Option) (*OpenAIProvider, error)   { return New("gpt-5.6-sol", opts...) }
func GPT5_6Terra(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.6-terra", opts...) }
func GPT5_6Luna(opts ...Option) (*OpenAIProvider, error)  { return New("gpt-5.6-luna", opts...) }

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

// Tier aliases — provider-agnostic shortcuts for common use cases.
func Cheapest(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.6-luna", opts...) }
func Standard(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.6-terra", opts...) }
func Smartest(opts ...Option) (*OpenAIProvider, error) { return New("gpt-5.6-sol", opts...) }
