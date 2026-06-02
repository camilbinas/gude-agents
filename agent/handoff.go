package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// handoffKey is the Context key for storing the HandoffRequest.
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

// GetHandoffRequest extracts the HandoffRequest from a *Context.
// Returns nil, false if no handoff was requested.
func GetHandoffRequest(c *Context) (*HandoffRequest, bool) {
	if c == nil {
		return nil, false
	}
	v, ok := c.Get(handoffKey{})
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

			if c := FromContext(ctx); c != nil {
				c.Set(handoffKey{}, &HandoffRequest{
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
// Cumulative token usage is available via c.Usage() after the call returns.
func (a *Agent) Resume(c *Context, hr *HandoffRequest, humanResponse string, cb StreamCallback) error {
	messages := make([]Message, len(hr.Messages))
	copy(messages, hr.Messages)

	// Apply input guardrails to the human response.
	msg := humanResponse
	for _, g := range a.inputGuardrails {
		var err error
		msg, err = g(c, msg)
		if err != nil {
			return &GuardrailError{Direction: "input", Cause: err}
		}
	}

	messages = append(messages, Message{
		Role:    RoleUser,
		Content: []ContentBlock{TextBlock{Text: msg}},
	})

	// Use the conversation ID from the handoff request so Resume targets
	// the correct conversation even when the agent has a different default.
	convID := hr.ConversationID
	if convID == "" {
		convID = resolveConversationID(c, a.conversationID)
	}

	// Acquire the per-conversation lock so that the Save region inside runLoop
	// is serialized with respect to Re_Entry_Turns and other concurrent invocations
	// on the same Conversation_ID (Req 7.1, 7.3, 7.4).
	if a.backgroundRegistry != nil && a.conversation != nil && convID != "" {
		m := a.backgroundRegistry.lockFor(convID)
		m.Lock()
		defer m.Unlock()
	}

	// Use base instructions — RAG context was already applied in the original invocation
	// and is reflected in the conversation history.
	// Resolve inference config for the resumed invocation.
	mergedInferenceCfg := mergeInferenceConfig(a.inferenceConfig, c.InferenceConfig())
	if err := validateInferenceConfig(mergedInferenceCfg); err != nil {
		return fmt.Errorf("inference config: %w", err)
	}

	h := a.hooks(c)
	usage, _, err := a.runLoop(c, convID, messages, 0, a.instructionsFor(c), mergedInferenceCfg, cb, &h, nil)

	// Store cumulative usage on the Context for caller access.
	c.setUsage(usage)

	// On successful resume, remove the persisted HandoffRequest if a store is configured.
	if err == nil && a.handoffStore != nil && convID != "" {
		_ = a.handoffStore.DeleteHandoff(c, convID)
	}

	return err
}

// ResumeInvoke is a convenience wrapper over Resume that collects streamed
// chunks into a single string.
func (a *Agent) ResumeInvoke(c *Context, hr *HandoffRequest, humanResponse string) (string, error) {
	var result string
	err := a.Resume(c, hr, humanResponse, func(chunk string) {
		result += chunk
	})
	return result, err
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
