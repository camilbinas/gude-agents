package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// ErrHandoffRequested is returned when an agent calls the handoff tool.
// The caller should inspect the HandoffRequest via GetHandoffRequest,
// collect the needed input, then call Agent.Resume to continue.
var ErrHandoffRequested = errors.New("handoff requested")

// handoffKey is the InvocationContext key for storing the HandoffRequest.
type handoffKey struct{}

// handoffSentinelHuman is the magic result that signals a human handoff.
const handoffSentinelHuman = "__human_handoff__"

// HandoffRequest captures why the agent paused and what it needs from a human.
type HandoffRequest struct {
	// Reason explains why the agent is handing off.
	Reason string
	// Question is the specific ask for the human.
	Question string
	// ConversationID is the conversation this handoff belongs to,
	// so Resume can target the correct conversation.
	ConversationID string
	// Messages is the full conversation state at the point of handoff,
	// allowing the caller to persist it and resume later.
	Messages []Message
}

// GetHandoffRequest extracts the HandoffRequest from an InvocationContext.
// Returns nil, false if no handoff was requested.
func GetHandoffRequest(ic *InvocationContext) (*HandoffRequest, bool) {
	if ic == nil {
		return nil, false
	}
	v, ok := ic.Get(handoffKey{})
	if !ok {
		return nil, false
	}
	hr, ok := v.(*HandoffRequest)
	return hr, ok
}

// NewHandoffTool creates a tool that lets an agent pause execution and request
// human input. When the LLM calls this tool, InvokeStream returns
// ErrHandoffRequested and the HandoffRequest is available via GetHandoffRequest.
//
// The name parameter sets the tool name exposed to the LLM. The description
// parameter is appended to the base tool description to define when the
// handoff should occur.
//
//	agent.New(provider, instructions, []tool.Tool{
//	    agent.NewHandoffTool("request_human_input", "Hand off when the user requests a refund over $500."),
//	})
func NewHandoffTool(name, description string) tool.Tool {
	base := "Pause execution and ask a human for input, a decision, or approval. " +
		"Use when you need information you cannot determine on your own."
	if description != "" {
		base += " " + description
	}
	return tool.NewRaw(
		name,
		base,
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{
					"type":        "string",
					"description": "Why you need human input",
				},
				"question": map[string]any{
					"type":        "string",
					"description": "The specific question or request for the human",
				},
			},
			"required": []string{"reason", "question"},
		},
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var params struct {
				Reason   string `json:"reason"`
				Question string `json:"question"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("invalid handoff input: %w", err)
			}

			if ic := GetInvocationContext(ctx); ic != nil {
				ic.Set(handoffKey{}, &HandoffRequest{
					Reason:   params.Reason,
					Question: params.Question,
				})
			}
			return handoffSentinelHuman, nil
		},
	)
}

// Resume continues an agent invocation after a human provides input.
// It restores the conversation from the HandoffRequest and appends the
// human's response as a new user message before re-entering the agent loop.
func (a *Agent) Resume(ctx context.Context, hr *HandoffRequest, humanResponse string, cb StreamCallback) (TokenUsage, error) {
	messages := make([]Message, len(hr.Messages))
	copy(messages, hr.Messages)

	messages = append(messages, Message{
		Role:    RoleUser,
		Content: []ContentBlock{TextBlock{Text: humanResponse}},
	})

	// Use the conversation ID from the handoff request so Resume targets
	// the correct conversation even when the agent has a different default.
	convID := hr.ConversationID
	if convID == "" {
		convID = ResolveConversationID(ctx, a.conversationID)
	}

	// Use base instructions — RAG context was already applied in the original invocation
	// and is reflected in the conversation history.
	// Resolve inference config for the resumed invocation.
	perInvocationCfg := GetInferenceConfig(ctx)
	mergedInferenceCfg := mergeInferenceConfig(a.inferenceConfig, perInvocationCfg)
	if err := validateInferenceConfig(mergedInferenceCfg); err != nil {
		return TokenUsage{}, fmt.Errorf("inference config: %w", err)
	}

	return a.runLoop(ctx, convID, messages, 0, a.instructions, mergedInferenceCfg, cb, &hooks{
		tracing: a.tracingHook,
		metrics: a.metricsHook,
		logging: a.loggingHook,
	})
}

// ResumeInvoke is a convenience wrapper over Resume that collects streamed
// chunks into a single string.
func (a *Agent) ResumeInvoke(ctx context.Context, hr *HandoffRequest, humanResponse string) (string, TokenUsage, error) {
	var result string
	usage, err := a.Resume(ctx, hr, humanResponse, func(chunk string) {
		result += chunk
	})
	return result, usage, err
}

// isHandoffResult checks if any tool result is a human handoff sentinel.
func isHandoffResult(results []ToolResultBlock) bool {
	for _, r := range results {
		if r.Content == handoffSentinelHuman {
			return true
		}
	}
	return false
}
