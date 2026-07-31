package anthropic

// Claude models (direct Anthropic API).
func ClaudeHaiku4_5(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-haiku-4-5", opts...)
}
func ClaudeSonnet4_5(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-sonnet-4-5", opts...)
}
func ClaudeSonnet4_6(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-sonnet-4-6", opts...)
}
func ClaudeSonnet5(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-sonnet-5", opts...)
}
func ClaudeOpus4_5(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-opus-4-5", opts...)
}
func ClaudeOpus4_6(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-opus-4-6", opts...)
}
func ClaudeOpus4_7(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-opus-4-7", opts...)
}
func ClaudeOpus4_8(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-opus-4-8", opts...)
}
func ClaudeOpus5(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-opus-5", opts...)
}
func ClaudeFable5(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-fable-5", opts...)
}

// Tier aliases — provider-agnostic shortcuts for common use cases.
// Smartest is Opus 5 rather than Fable 5: Fable refuses far more often, which is
// poor behaviour for a default.
func Cheapest(opts ...Option) (*AnthropicProvider, error) { return New("claude-haiku-4-5", opts...) }
func Standard(opts ...Option) (*AnthropicProvider, error) { return New("claude-sonnet-5", opts...) }
func Smartest(opts ...Option) (*AnthropicProvider, error) { return New("claude-opus-5", opts...) }
