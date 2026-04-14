package anthropic

// Claude models (direct Anthropic API).
// Documented in docs/providers.md — update when adding or removing models.
func ClaudeHaiku4_5() (*AnthropicProvider, error)  { return New("claude-haiku-4-5") }
func ClaudeSonnet4_5() (*AnthropicProvider, error) { return New("claude-sonnet-4-5") }
func ClaudeSonnet4_6() (*AnthropicProvider, error) { return New("claude-sonnet-4-6") }
func ClaudeOpus4_5() (*AnthropicProvider, error)   { return New("claude-opus-4-5") }
func ClaudeOpus4_6() (*AnthropicProvider, error)   { return New("claude-opus-4-6") }

// Tier aliases — provider-agnostic shortcuts for common use cases.
func Cheapest(opts ...Option) (*AnthropicProvider, error) { return New("claude-haiku-4-5", opts...) }
func Standard(opts ...Option) (*AnthropicProvider, error) { return New("claude-sonnet-4-6", opts...) }
func Smartest(opts ...Option) (*AnthropicProvider, error) { return New("claude-opus-4-6", opts...) }
