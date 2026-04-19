package agent

import (
	"context"
	"encoding/json"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// Role identifies the sender of a message.
// Documented in docs/message-types.md — update when changing constants or type.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single turn in the conversation.
// Documented in docs/message-types.md — update when changing fields.
type Message struct {
	Role    Role
	Content []ContentBlock
}

// ContentBlock is a sealed union type for message content.
// Documented in docs/message-types.md — update when adding implementations.
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
}

// Each block type implements the sealed ContentBlock interface.
func (TextBlock) contentBlock()       {}
func (ToolUseBlock) contentBlock()    {}
func (ToolResultBlock) contentBlock() {}

// TokenUsage records token consumption for a single Provider call.
// Documented in docs/agent-api.md and docs/message-types.md — update when changing fields.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

// Total returns the sum of input and output tokens.
func (u TokenUsage) Total() int {
	return u.InputTokens + u.OutputTokens
}

// ConverseParams holds the inputs for a Provider call.
// Documented in docs/message-types.md — update when changing fields.
type ConverseParams struct {
	Messages         []Message
	System           string
	ToolConfig       []tool.Spec
	ToolChoice       *tool.Choice     // nil = provider default (auto)
	ThinkingCallback ThinkingCallback // optional; called with thinking chunks during streaming
}

// ProviderResponse is the result of an LLM call.
// Documented in docs/message-types.md — update when changing fields.
type ProviderResponse struct {
	Text      string
	ToolCalls []tool.Call
	Usage     TokenUsage
	Metadata  map[string]any // optional provider-specific extras (e.g. "thinking")
}

// StreamCallback receives incremental text chunks during streaming.
// Documented in docs/message-types.md — update when changing signature.
type StreamCallback func(chunk string)

// ThinkingCallback receives incremental thinking/reasoning chunks during streaming.
// Called in real-time as the model reasons, before the final answer is produced.
// Only invoked when the provider has thinking enabled (e.g. WithThinking, WithReasoningEffort).
type ThinkingCallback func(chunk string)

// Provider abstracts an LLM backend.
// Documented in docs/providers.md — update when changing interface methods.
type Provider interface {
	Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error)
	ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error)
}

// Capabilities describes what features a Provider's model supports.
type Capabilities struct {
	ToolUse    bool
	ToolChoice bool
	TokenUsage bool
}

// CapabilityReporter is an optional interface a Provider can implement to
// advertise what its underlying model supports.
// Documented in docs/providers.md — update when changing interface or Capabilities struct.
type CapabilityReporter interface {
	Capabilities() Capabilities
}

// ModelIdentifier is an optional interface a Provider can implement to
// expose the underlying model ID.
type ModelIdentifier interface {
	ModelID() string
}
