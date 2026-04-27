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
func ClaudeOpus4_5(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-opus-4-5", opts...)
}
func ClaudeOpus4_6(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-opus-4-6", opts...)
}
func ClaudeOpus4_7(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-opus-4-7", opts...)
}

// Tier aliases — provider-agnostic shortcuts for common use cases.
func Cheapest(opts ...Option) (*AnthropicProvider, error) { return New("claude-haiku-4-5", opts...) }
func Standard(opts ...Option) (*AnthropicProvider, error) { return New("claude-sonnet-4-6", opts...) }
func Smartest(opts ...Option) (*AnthropicProvider, error) { return New("claude-opus-4-7", opts...) }
