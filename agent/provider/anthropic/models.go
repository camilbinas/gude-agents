package anthropic

import "github.com/camilbinas/gude-agents/agent"

// modelProfile is what Gude knows about one model ID, independent of how the
// caller constructs the provider.
type modelProfile struct {
	capabilities agent.ModelCapabilities
}

// catalog is the single source of truth for known Claude model IDs. New
// consults it before applying caller options, so New("claude-sonnet-5-5") and
// ClaudeSonnet5_5() are identical.
//
// Entries hold only facts that are documented for the direct Anthropic API at
// https://platform.claude.com/docs/en/models/<model>/overview and supported by
// this adapter. A field that is not listed is intentionally Unknown. Numeric
// limits are advisory metadata, not enforced limits.
//
// Tool-use support is recorded only for the models whose pages or the models
// overview state it for the current lineup; legacy pages do not, so those
// models report ToolUse as Unknown.
var catalog = map[string]modelProfile{
	"claude-haiku-4-5":  {capabilities: agent.ModelCapabilities{ContextWindowTokens: 200_000, MaxOutputTokens: 64_000}},
	"claude-sonnet-4-5": {capabilities: agent.ModelCapabilities{ContextWindowTokens: 200_000, MaxOutputTokens: 64_000}},
	"claude-sonnet-4-6": {capabilities: agent.ModelCapabilities{ContextWindowTokens: 1_000_000, MaxOutputTokens: 128_000}},
	"claude-sonnet-5":   {capabilities: agent.ModelCapabilities{ContextWindowTokens: 1_000_000, MaxOutputTokens: 128_000}},
	"claude-opus-4-5":   {capabilities: agent.ModelCapabilities{ContextWindowTokens: 200_000, MaxOutputTokens: 64_000}},
	"claude-opus-4-6":   {capabilities: agent.ModelCapabilities{ContextWindowTokens: 1_000_000, MaxOutputTokens: 128_000}},
	"claude-opus-4-7":   {capabilities: agent.ModelCapabilities{ContextWindowTokens: 1_000_000, MaxOutputTokens: 128_000}},
	"claude-opus-4-8":   {capabilities: agent.ModelCapabilities{ContextWindowTokens: 1_000_000, MaxOutputTokens: 128_000}},
	"claude-opus-5":     {capabilities: agent.ModelCapabilities{ContextWindowTokens: 1_000_000, MaxOutputTokens: 128_000}},
	"claude-fable-5":    {capabilities: agent.ModelCapabilities{ContextWindowTokens: 1_000_000, MaxOutputTokens: 128_000}},
	"claude-opus-5-5":   {capabilities: claude55Capabilities},
	"claude-sonnet-5-5": {capabilities: claude55Capabilities},
}

// claude55Capabilities are shared by the Claude 5.5 models: automatic tool
// choice only, because forced and named tool choice are not supported.
var claude55Capabilities = agent.ModelCapabilities{
	ContextWindowTokens: 1_000_000,
	MaxOutputTokens:     128_000,
	ToolUse:             agent.Supported,
	ToolChoice: agent.ToolChoiceCapabilities{
		Auto:     agent.Supported,
		Required: agent.Unsupported,
		Specific: agent.Unsupported,
	},
}

// Named Claude models. Each delegates to New with its canonical model ID; the
// catalog supplies the capability metadata.
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
func ClaudeOpus5(opts ...Option) (*AnthropicProvider, error)  { return New("claude-opus-5", opts...) }
func ClaudeFable5(opts ...Option) (*AnthropicProvider, error) { return New("claude-fable-5", opts...) }
func ClaudeOpus5_5(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-opus-5-5", opts...)
}
func ClaudeSonnet5_5(opts ...Option) (*AnthropicProvider, error) {
	return New("claude-sonnet-5-5", opts...)
}
