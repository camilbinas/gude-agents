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

// Each block type implements the sealed ContentBlock interface.
func (TextBlock) contentBlock()       {}
func (ToolUseBlock) contentBlock()    {}
func (ToolResultBlock) contentBlock() {}

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

// ModelRequest holds the inputs for a Provider call.
type ModelRequest struct {
	Messages        []Message
	System          string
	Tools           []tool.Spec
	ToolChoice      *tool.Choice     // nil = provider default (auto)
	InferenceConfig *InferenceConfig // nil = use provider defaults
	CachingEnabled  bool             // when true, providers attach cache breakpoints automatically
}

// ModelResponse is the result of a model call.
type ModelResponse struct {
	Text      string
	ToolCalls []tool.Call
	Usage     TokenUsage
	Metadata  map[string]any // optional provider-specific extras (e.g. "thinking")
}

// ModelEventType identifies an incremental model output event.
type ModelEventType string

const (
	ModelEventText     ModelEventType = "text"
	ModelEventThinking ModelEventType = "thinking"
)

// ModelEvent is an incremental text or thinking event emitted by a Provider.
type ModelEvent struct {
	Type ModelEventType
	Text string
}

// Provider abstracts an LLM backend. emit may be nil.
type Provider interface {
	Name() string
	Stream(ctx context.Context, req ModelRequest, emit func(ModelEvent)) (*ModelResponse, error)
}

// ModelIdentifier is an optional interface a Provider can implement to
// expose the underlying model ID.
type ModelIdentifier interface {
	ModelID() string
}

// Invoker abstracts anything that can handle a user message and return a
// Result. *Agent satisfies this interface and tests can provide fakes.
type Invoker interface {
	Invoke(c *Context, input string) (Result, error)
}

// compile-time check: *Agent implements Invoker.
var _ Invoker = (*Agent)(nil)
