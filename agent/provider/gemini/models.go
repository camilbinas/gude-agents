package gemini

// Gemini models (Google GenAI API).
// Documented in docs/providers.md — update when adding or removing models.
func Gemini25Pro(opts ...Option) (*GeminiProvider, error)   { return New("gemini-2.5-pro", opts...) }
func Gemini25Flash(opts ...Option) (*GeminiProvider, error) { return New("gemini-2.5-flash", opts...) }
func Gemini25FlashLite(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-2.5-flash-lite", opts...)
}
func Gemini3Flash(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-3-flash-preview", opts...)
}
func Gemini31Pro(opts ...Option) (*GeminiProvider, error) {
	return New("gemini-3.1-pro-preview", opts...)
}

// Tier aliases — provider-agnostic shortcuts for common use cases.
func Cheapest(opts ...Option) (*GeminiProvider, error) { return New("gemini-2.5-flash-lite", opts...) }
func Standard(opts ...Option) (*GeminiProvider, error) { return New("gemini-2.5-flash", opts...) }
func Smartest(opts ...Option) (*GeminiProvider, error) { return New("gemini-2.5-pro", opts...) }
