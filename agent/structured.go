package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

const structuredOutputToolName = "structured_output"

// InvokeStructured forces the LLM to return a JSON response conforming to T.
// It applies input guardrails, loads/saves memory, and applies output guardrails
// in the same way as InvokeStream.
// Documented in docs/structured-output.md — update when changing signature or mechanism.
func InvokeStructured[T any](ctx context.Context, a *Agent, userMessage string) (T, TokenUsage, error) {
	var zero T

	// 1. Apply input guardrails.
	msg := userMessage
	for _, g := range a.inputGuardrails {
		var err error
		msg, err = g(ctx, msg)
		if err != nil {
			return zero, TokenUsage{}, &GuardrailError{Direction: "input", Cause: err}
		}
	}

	// 2. Load conversation history from memory if configured.
	convID := ResolveConversationID(ctx, a.conversationID)
	var messages []Message
	if a.memory != nil {
		history, err := a.memory.Load(ctx, convID)
		if err != nil {
			return zero, TokenUsage{}, fmt.Errorf("structured output: memory load: %w", err)
		}
		messages = history
	}
	// 3. RAG: inject retrieved context as a separate user/assistant turn before the
	// actual user message, keeping untrusted content isolated from the user query.
	if a.retriever != nil {
		docs, err := a.retriever.Retrieve(ctx, msg)
		if err != nil {
			return zero, TokenUsage{}, fmt.Errorf("structured output: retriever: %w", err)
		}
		if len(docs) > 0 {
			formatter := a.contextFormatter
			if formatter == nil {
				formatter = DefaultContextFormatter
			}
			if contextStr := formatter(docs); contextStr != "" {
				messages = append(messages,
					Message{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: contextStr}}},
					Message{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "Understood. I will use this context to answer your question."}}},
				)
			}
		}
	}
	messages = append(messages, Message{
		Role:    RoleUser,
		Content: []ContentBlock{TextBlock{Text: msg}},
	})

	// 4. Call the provider with forced tool choice.
	schema := tool.GenerateSchema[T]()
	responseToolSpec := tool.Spec{
		Name:        structuredOutputToolName,
		Description: "Respond with structured JSON output conforming to the schema.",
		InputSchema: schema,
	}

	params := ConverseParams{
		Messages:   messages,
		System:     a.instructions,
		ToolConfig: []tool.Spec{responseToolSpec},
		ToolChoice: &tool.Choice{
			Mode: tool.ChoiceTool,
			Name: structuredOutputToolName,
		},
	}

	resp, err := a.provider.Converse(ctx, params)
	if err != nil {
		return zero, TokenUsage{}, &ProviderError{Cause: err}
	}

	usage := resp.Usage

	if len(resp.ToolCalls) == 0 {
		return zero, usage, fmt.Errorf("structured output: LLM did not return a tool call to %s", structuredOutputToolName)
	}

	var found *tool.Call
	for i := range resp.ToolCalls {
		if resp.ToolCalls[i].Name == structuredOutputToolName {
			found = &resp.ToolCalls[i]
			break
		}
	}
	if found == nil {
		return zero, usage, fmt.Errorf("structured output: LLM called tool %q instead of %s", resp.ToolCalls[0].Name, structuredOutputToolName)
	}

	// 5. Apply output guardrails to the raw JSON text before deserializing.
	rawText := string(found.Input)
	for _, g := range a.outputGuardrails {
		rawText, err = g(ctx, rawText)
		if err != nil {
			return zero, usage, &GuardrailError{Direction: "output", Cause: err}
		}
	}

	// 6. Deserialize the (possibly guardrail-transformed) JSON.
	var result T
	if err := json.Unmarshal([]byte(rawText), &result); err != nil {
		return zero, usage, fmt.Errorf("structured output: failed to deserialize response: %w", err)
	}

	// 7. Save the exchange to memory if configured.
	if a.memory != nil {
		assistantMsg := Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{TextBlock{Text: rawText}},
		}
		_ = a.memory.Save(ctx, convID, append(messages, assistantMsg))
	}

	return result, usage, nil
}
