package agent

import (
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent/tool"
)

const structuredOutputToolName = "structured_output"

// structuredOutputToolChoice chooses the strongest compatible tool-selection
// mode. Unknown metadata keeps the named forced-tool path. A model that
// explicitly lacks named choice but supports automatic selection receives the
// sole schema tool with a nil choice; providers interpret nil as their default
// automatic behavior and can omit unsupported choice fields entirely.
func structuredOutputToolChoice(caps ModelCapabilities) (*tool.Choice, error) {
	if caps.ToolUse == Unsupported {
		return nil, &StructuredOutputError{
			Reason: "unsupported_capability",
			Cause:  fmt.Errorf("%w: tool use", ErrStructuredOutputUnsupported),
		}
	}
	if caps.ToolChoice.Specific != Unsupported {
		return &tool.Choice{Mode: tool.ChoiceTool, Name: structuredOutputToolName}, nil
	}
	if caps.ToolChoice.Auto != Unsupported {
		return nil, nil
	}
	return nil, &StructuredOutputError{
		Reason: "unsupported_capability",
		Cause:  fmt.Errorf("%w: neither named nor automatic tool choice", ErrStructuredOutputUnsupported),
	}
}

// InvokeSchema runs the shared structured-output lifecycle for a JSON schema.
// The model's output is checked against the supported schema subset (see
// ValidateToolInput: JSON validity, type, enum, required, properties, items)
// and then passed to decode; both happen before conversation persistence, so
// output that fails either check is never saved. Other JSON Schema keywords
// are sent to the model but not enforced.
// Most callers should use structured.Invoke[T] from package agent/structured.
func (a *Agent) InvokeSchema(c *Context, userMessage string, schema map[string]any, decode func(json.RawMessage) error) (Result, error) {
	if c == nil {
		return Result{}, ErrNilContext
	}
	if decode == nil {
		return Result{}, fmt.Errorf("structured output decoder is required")
	}
	toolChoice, err := structuredOutputToolChoice(CapabilitiesOf(a.provider))
	if err != nil {
		return Result{}, err
	}
	inv := c.forInvocation(c, &invocationRuntime{})
	convID := inv.ConversationID()
	return a.lifecycle(inv, convID, userMessage, func(r *run) (Result, error) {
		return r.structuredTurn(userMessage, schema, toolChoice, func(raw []byte) error {
			return decode(json.RawMessage(raw))
		})
	})
}

// structuredTurn runs a single schema-tool model call, applies output
// guardrails to the raw JSON, decodes it with decode and only then persists
// the turn. Result.Text is the guardrail-processed JSON.
func (r *run) structuredTurn(userMessage string, schema map[string]any, toolChoice *tool.Choice, decode func([]byte) error) (Result, error) {
	a, c, h := r.a, r.c, &r.h

	messages, ragStart, err := r.prepareTurn(userMessage)
	if err != nil {
		return Result{}, fmt.Errorf("structured output: %w", err)
	}
	cfg, err := r.inferenceConfig()
	if err != nil {
		return Result{}, fmt.Errorf("structured output: %w", err)
	}

	system := a.instructionsFor(c)
	structuredSpec := tool.Spec{
		Name:        structuredOutputToolName,
		Description: "Respond with structured JSON output conforming to the schema.",
		InputSchema: schema,
	}
	modelMessages, err := r.modelMessages(c, messages, ragStart, system, []tool.Spec{structuredSpec})
	if err != nil {
		return Result{}, fmt.Errorf("structured output: %w", err)
	}
	modelReq := ModelRequest{
		Messages:        modelMessages,
		System:          system,
		Tools:           []tool.Spec{structuredSpec},
		ToolChoice:      toolChoice,
		InferenceConfig: cfg,
		CachingEnabled:  a.cachingEnabled,
	}

	provC, provF := h.onModelStart(c, ModelCallRecord{
		ModelID:         a.modelID(),
		Iteration:       1,
		System:          modelReq.System,
		MessageCount:    len(modelReq.Messages),
		InferenceConfig: modelReq.InferenceConfig,
	})
	resp, err := a.callProviderWithRetry(provC, r.convID, modelReq, nil)
	if err != nil {
		provF.finish(err, TokenUsage{}, 0, "")
		return Result{}, &ProviderError{Cause: err}
	}
	provF.finish(nil, resp.Usage, len(resp.ToolCalls), "")
	cumulative := c.rt.addUsage(resp.Usage)
	if a.tokenBudget > 0 && cumulative.Total() > a.tokenBudget {
		return Result{}, ErrTokenBudgetExceeded
	}

	if len(resp.ToolCalls) == 0 {
		return Result{}, &StructuredOutputError{Reason: "no_tool_call"}
	}
	var found *tool.Call
	for i := range resp.ToolCalls {
		if resp.ToolCalls[i].Name == structuredOutputToolName {
			found = &resp.ToolCalls[i]
			break
		}
	}
	if found == nil {
		return Result{}, &StructuredOutputError{Reason: "wrong_tool"}
	}

	rawText := string(found.Input)
	for _, g := range a.outputGuardrails {
		gC, gf := h.onGuardrailStart(c, "output", rawText)
		rawText, err = g(gC, rawText)
		gf.finish(err, rawText)
		if err != nil {
			return Result{}, &GuardrailError{Direction: "output", Cause: err}
		}
	}
	// Validate the final (guardrail-processed) JSON against the requested
	// schema before decoding or persisting it. Provider-native structured
	// output is not trusted: a successful structured invocation guarantees the
	// returned JSON satisfies the supported schema subset (ValidateToolInput)
	// and decodes into the target. Keywords outside that subset are not
	// enforced. Syntactically invalid JSON is a decode failure, not a schema
	// violation.
	if !json.Valid([]byte(rawText)) {
		return Result{}, &StructuredOutputError{Reason: "deserialize", Cause: fmt.Errorf("invalid JSON")}
	}
	if err := ValidateToolInput(schema, json.RawMessage(rawText)); err != nil {
		return Result{}, &StructuredOutputError{Reason: "schema_validation", Cause: err}
	}
	if err := decode([]byte(rawText)); err != nil {
		return Result{}, &StructuredOutputError{Reason: "deserialize", Cause: err}
	}

	if r.hasConversation() {
		toSave := append(persisted(messages, ragStart), Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{TextBlock{Text: rawText}},
		})
		if _, err := r.saveConversation(toSave, c.rt.totalUsage()); err != nil {
			return Result{}, fmt.Errorf("structured output: %w", err)
		}
	}
	return Result{Text: rawText, StopReason: StopEndTurn, Metadata: resp.Metadata}, nil
}
