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
// Entries hold only facts documented for the direct Anthropic API at
// https://platform.claude.com/docs/en/about-claude/models/overview and its
// pricing and thinking pages, and supported by this adapter. The adapter sends
// auto, any (Required) and tool (Specific) tool_choice, so each ToolChoice
// field reflects what the model itself accepts. A field without a source stays
// Unknown. Numeric limits are advisory metadata, not enforced limits.
var catalog = map[string]modelProfile{
	// 200K context, 64K output; forced tool use accepted (any, tool).
	"claude-haiku-4-5":  {capabilities: caps200kFullToolChoice},
	"claude-sonnet-4-5": {capabilities: caps200kFullToolChoice},
	"claude-opus-4-5":   {capabilities: caps200kFullToolChoice},
	// 1M context, 128K output; forced tool use accepted (any, tool).
	"claude-sonnet-4-6": {capabilities: caps1mFullToolChoice},
	"claude-sonnet-5":   {capabilities: caps1mFullToolChoice},
	"claude-opus-4-6":   {capabilities: caps1mFullToolChoice},
	"claude-opus-4-7":   {capabilities: caps1mFullToolChoice},
	"claude-opus-4-8":   {capabilities: caps1mFullToolChoice},
	"claude-opus-5":     {capabilities: caps1mFullToolChoice},
	"claude-fable-5":    {capabilities: caps1mFullToolChoice},
	// 1M context, 128K output; forced tool use returns 400 (auto only).
	"claude-opus-5-5":   {capabilities: caps1mAutoOnlyToolChoice},
	"claude-sonnet-5-5": {capabilities: caps1mAutoOnlyToolChoice},
}

// fullToolChoice is the mode set for models that accept auto, any and tool.
var fullToolChoice = agent.ToolChoiceCapabilities{
	Auto:     agent.Supported,
	Required: agent.Supported,
	Specific: agent.Supported,
}

// autoOnlyToolChoice is the mode set for models whose adaptive thinking rejects
// forced tool use (any/tool) with a 400 error, leaving only auto.
var autoOnlyToolChoice = agent.ToolChoiceCapabilities{
	Auto:     agent.Supported,
	Required: agent.Unsupported,
	Specific: agent.Unsupported,
}

var caps200kFullToolChoice = agent.ModelCapabilities{
	ContextWindowTokens: 200_000,
	MaxOutputTokens:     64_000,
	ToolUse:             agent.Supported,
	ToolChoice:          fullToolChoice,
}

var caps1mFullToolChoice = agent.ModelCapabilities{
	ContextWindowTokens: 1_000_000,
	MaxOutputTokens:     128_000,
	ToolUse:             agent.Supported,
	ToolChoice:          fullToolChoice,
}

var caps1mAutoOnlyToolChoice = agent.ModelCapabilities{
	ContextWindowTokens: 1_000_000,
	MaxOutputTokens:     128_000,
	ToolUse:             agent.Supported,
	ToolChoice:          autoOnlyToolChoice,
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
