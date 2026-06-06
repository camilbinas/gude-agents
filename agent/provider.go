package agent

import (
	"context"
	"encoding/json"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// Role identifies the sender of a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single turn in the conversation.
type Message struct {
	Role    Role
	Content []ContentBlock
}

// ContentBlock is a sealed union type for message content.
type ContentBlock interface {
	contentBlock() // sealed marker
}

// TextBlock holds plain text content.
type TextBlock struct {
	Text string
}

// ToolUseBlock represents the LLM requesting a tool call.
type ToolUseBlock struct {
	ToolUseID string
	Name      string
	Input     json.RawMessage
}

// ToolResultBlock holds the result of a tool execution.
type ToolResultBlock struct {
	ToolUseID string
	Content   string
	IsError   bool
	Images    []ImageBlock // optional images returned by the tool
}

// CacheableBlock wraps another ContentBlock and signals that this position
// in the message content list is a desired cache breakpoint.
// Providers that support explicit breakpoints (Anthropic, Bedrock/Claude) will
// attach cache_control to the underlying block. Providers that do not support
// breakpoints (OpenAI, Gemini) will unwrap Inner and treat it as a plain block.
type CacheableBlock struct {
	Inner ContentBlock
}

// Each block type implements the sealed ContentBlock interface.
func (TextBlock) contentBlock()       {}
func (ToolUseBlock) contentBlock()    {}
func (ToolResultBlock) contentBlock() {}
func (CacheableBlock) contentBlock()  {}

// TokenUsage records token consumption for a single Provider call.
type TokenUsage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int // tokens served from the prompt cache (0 if not applicable)
	CacheWriteTokens int // tokens written to the prompt cache (0 if not applicable)
}

// Total returns the sum of input and output tokens.
// Cache tokens are excluded — they represent re-used content and should be
// tracked separately for cost analysis.
func (u TokenUsage) Total() int {
	return u.InputTokens + u.OutputTokens
}

// InferenceConfig groups LLM inference/sampling parameters.
// All fields are optional — nil means "use provider default."
type InferenceConfig struct {
	Temperature   *float64
	TopP          *float64
	TopK          *int
	StopSequences []string
	MaxTokens     *int
}

// ConverseParams holds the inputs for a Provider call.
type ConverseParams struct {
	Messages         []Message
	System           string
	ToolConfig       []tool.Spec
	ToolChoice       *tool.Choice     // nil = provider default (auto)
	ThinkingCallback ThinkingCallback // optional; called with thinking chunks during streaming
	InferenceConfig  *InferenceConfig // nil = use provider defaults
}

// ProviderResponse is the result of an LLM call.
type ProviderResponse struct {
	Text      string
	ToolCalls []tool.Call
	Usage     TokenUsage
	Metadata  map[string]any // optional provider-specific extras (e.g. "thinking")
}

// StreamCallback receives incremental text chunks during streaming.
type StreamCallback func(chunk string)

// ThinkingCallback receives incremental thinking/reasoning chunks during streaming.
// Called in real-time as the model reasons, before the final answer is produced.
// Only invoked when the provider has thinking enabled (e.g. WithThinking, WithReasoningEffort).
type ThinkingCallback func(chunk string)

// Provider abstracts an LLM backend.
type Provider interface {
	Name() string
	Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error)
	ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error)
}

// ModelIdentifier is an optional interface a Provider can implement to
// expose the underlying model ID.
type ModelIdentifier interface {
	ModelID() string
}

// Invoker abstracts anything that can handle a user message and return a
// text response. *Agent satisfies this interface.
// Used by graph.AgentNode, graph.LLMRouter, and useful for testing.
type Invoker interface {
	Invoke(c *Context, userMessage string) (string, error)
}

// compile-time check: *Agent implements Invoker.
var _ Invoker = (*Agent)(nil)

// compile-time check: CacheableBlock implements ContentBlock.
var _ ContentBlock = CacheableBlock{}
