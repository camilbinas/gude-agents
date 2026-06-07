package agent

import (
	"fmt"
)

// LoopConfig provides optional overrides for RunLoop behavior.
type LoopConfig struct {
	// ExtraMiddleware is prepended (outermost) to the agent's middleware chain.
	// Does not mutate the agent's middleware slice.
	ExtraMiddleware []Middleware
	// ToolResultInterceptor is called after each tool execution batch.
	// If it returns true, the loop stops and returns ErrLoopStopped.
	// The interceptor receives the tool results and may inspect them.
	ToolResultInterceptor func(results []ToolResultBlock) bool
	// SkipConversationSave prevents the loop from calling saveConversation.
	SkipConversationSave bool
}

// LoopParams holds the inputs for RunLoop.
type LoopParams struct {
	// Messages is the conversation history to send to the provider.
	Messages []Message
	// SystemPrompt overrides the agent's instructions if non-empty.
	SystemPrompt string
	// InferenceConfig overrides the agent's inference config if non-nil.
	InferenceConfig *InferenceConfig
	// StreamCallback receives streamed text chunks.
	StreamCallback StreamCallback
	// Config holds optional loop behavior overrides.
	Config LoopConfig
}

// RunLoop executes the agent's iteration loop with caller-supplied messages.
// It does NOT perform input guardrails, conversation loading, RAG retrieval,
// or conversation saving (unless SkipConversationSave is false in cfg).
// Returns cumulative token usage, the final assistant text, and an error.
// If a ToolResultInterceptor in cfg signals stop, returns ErrLoopStopped.
func (a *Agent) RunLoop(c *Context, params LoopParams) (TokenUsage, string, error) {
	// Resolve system prompt: use params override if non-empty, else context override (A/B), else agent's instructions.
	systemPrompt := a.instructionsFor(c)
	if params.SystemPrompt != "" {
		systemPrompt = params.SystemPrompt
	}

	// Resolve inference config: use params override if non-nil, else merge agent's
	// config with the per-invocation config from the Context.
	var inferenceConfig *InferenceConfig
	if params.InferenceConfig != nil {
		inferenceConfig = params.InferenceConfig
	} else {
		inferenceConfig = mergeInferenceConfig(a.inferenceConfig, c.InferenceConfig())
	}
	if err := validateInferenceConfig(inferenceConfig); err != nil {
		return TokenUsage{}, "", fmt.Errorf("inference config: %w", err)
	}

	// Resolve conversation ID from the Context.
	convID := resolveConversationID(c, a.conversationID)

	// Build internal loop config from LoopConfig.
	var cfg *runLoopConfig
	lc := params.Config
	if lc.ExtraMiddleware != nil || lc.ToolResultInterceptor != nil || lc.SkipConversationSave {
		cfg = &runLoopConfig{
			extraMiddleware:       lc.ExtraMiddleware,
			toolResultInterceptor: lc.ToolResultInterceptor,
			skipConversationSave:  lc.SkipConversationSave,
		}
	}

	h := a.hooks(c)
	usage, finalText, err := a.runLoop(c, convID, params.Messages, 0, systemPrompt, inferenceConfig, params.StreamCallback, &h, cfg, a.cachingEnabled)

	// Store cumulative usage on the Context for caller access.
	c.setUsage(usage)

	return usage, finalText, err
}
