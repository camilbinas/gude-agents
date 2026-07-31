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
	return New("gemini-3.1-flash-lite", opts...)
}
func Gemini31Pro(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-3.1-pro-preview", opts...)
}
func Gemini35Flash(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-3.5-flash", opts...)
}
func Gemini35FlashLite(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-3.5-flash-lite", opts...)
}
func Gemini36Flash(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-3.6-flash", opts...)
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
// Smartest is a preview ID because Gemini 3.5 Pro has not shipped and 3.1 Pro is
// the newest Pro-tier model.
func Cheapest(opts ...Option) (*GeminiProvider, error) { return New("gemini-3.5-flash-lite", opts...) }
func Standard(opts ...Option) (*GeminiProvider, error) { return New("gemini-3.6-flash", opts...) }
func Smartest(opts ...Option) (*GeminiProvider, error) { return New("gemini-3.1-pro-preview", opts...) }
