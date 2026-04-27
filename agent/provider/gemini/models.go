package gemini

import rag "github.com/camilbinas/gude-agents/agent/rag/gemini"

// Gemini models (Google GenAI API).
func Gemini25Pro(opts ...Option) (*GeminiProvider, error)   { return New("gemini-2.5-pro", opts...) }
func Gemini25Flash(opts ...Option) (*GeminiProvider, error) { return New("gemini-2.5-flash", opts...) }
func Gemini25FlashLite(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-2.5-flash-lite", opts...)
}
func Gemini3Flash(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-3-flash-preview", opts...)
}
func Gemini31FlashLite(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-3.1-flash-lite-preview", opts...)
}
func Gemini31Pro(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-3.1-pro-preview", opts...)
}

// Gemini embedding models.
// These forward to agent/rag/gemini — import that package directly for
// access to EmbedderOption and the Embedder type.
func GeminiEmbedding001(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.GeminiEmbedding001(opts...)
}
func GeminiEmbedding002(opts ...rag.EmbedderOption) (*rag.Embedder, error) {
	return rag.GeminiEmbedding002(opts...)
}

// Tier aliases — provider-agnostic shortcuts for common use cases.
func Cheapest(opts ...Option) (*GeminiProvider, error) { return New("gemini-2.5-flash-lite", opts...) }
func Standard(opts ...Option) (*GeminiProvider, error) { return New("gemini-2.5-flash", opts...) }
func Smartest(opts ...Option) (*GeminiProvider, error) { return New("gemini-2.5-pro", opts...) }
