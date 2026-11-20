package agent

import (
	"encoding/json"
	"errors"
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
			Reason: ReasonUnsupportedCapability,
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
		Reason: ReasonUnsupportedCapability,
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
func (a *Runtime) InvokeSchema(c *Context, userMessage string, schema map[string]any, decode func(json.RawMessage) error) (Result, error) {
	if c == nil {
		return Result{}, ErrNilContext
	}
	if decode == nil {
		return Result{}, fmt.Errorf("structured output decoder is required")
	}
	caps := CapabilitiesOf(a.provider)
	native := caps.NativeStructuredOutput == Supported
	var toolChoice *tool.Choice
	if !native {
		var err error
		if toolChoice, err = structuredOutputToolChoice(caps); err != nil {
			return Result{}, err
		}
	}
	inv := c.forInvocation(c, &invocationRuntime{})
	convID := inv.ConversationID()
	return a.lifecycle(inv, convID, userMessage, func(r *run) (Result, error) {
		return r.structuredTurn(userMessage, schema, native, toolChoice, func(raw []byte) error {
			return decode(json.RawMessage(raw))
		})
	})
}

// structuredTurn runs the structured-output lifecycle: one model call, output
// guardrails, schema validation and decode, retried up to a.structuredOutputRetries
// times with the failure fed back to the model. Only the final successful
// answer is persisted; usage, guardrails and hooks apply to every attempt.
// Result.Text is the guardrail-processed JSON.
func (r *run) structuredTurn(userMessage string, schema map[string]any, native bool, toolChoice *tool.Choice, decode func([]byte) error) (Result, error) {
	a, c := r.a, r.c

	messages, ragStart, err := r.prepareTurn(userMessage)
	if err != nil {
		return Result{}, fmt.Errorf("structured output: %w", err)
	}
	cfg, err := r.inferenceConfig()
	if err != nil {
		return Result{}, fmt.Errorf("structured output: %w", err)
	}

	system := a.instructionsFor(c)
	var tools []tool.Spec
	if !native {
		tools = []tool.Spec{{
			Name:        structuredOutputToolName,
			Description: "Respond with structured JSON output conforming to the schema.",
			InputSchema: schema,
		}}
	}
	baseMessages, err := r.modelMessages(c, messages, ragStart, system, tools)
	if err != nil {
		return Result{}, fmt.Errorf("structured output: %w", err)
	}

	// feedback holds the failed-attempt turns appended across retries; the base
	// conversation is never mutated, so only the final success is persisted.
	var feedback []Message
	attempts := a.structuredOutputRetries + 1
	for attempt := 1; ; attempt++ {
		rawText, meta, err := r.structuredAttempt(attempt, system, schema, tools, toolChoice, native, cfg, baseMessages, feedback, decode)
		if err == nil {
			if r.hasConversation() {
				toSave := append(persisted(messages, ragStart), Message{
					Role:    RoleAssistant,
					Content: []ContentBlock{TextBlock{Text: rawText}},
				})
				if err := r.saveConversation(toSave, c.rt.totalUsage()); err != nil {
					return Result{}, fmt.Errorf("structured output: %w", err)
				}
			}
			return Result{Text: rawText, StopReason: StopEndTurn, Metadata: meta}, nil
		}
		var soErr *StructuredOutputError
		if attempt >= attempts || !retryableStructuredError(err, &soErr) {
			return Result{}, err
		}
		feedback = append(feedback, structuredFeedback(native, soErr, rawText, attempt)...)
	}
}

// retryableStructuredError reports whether err is a StructuredOutputError whose
// reason is one the model can correct by trying again, and binds it into *dst.
func retryableStructuredError(err error, dst **StructuredOutputError) bool {
	if !errors.As(err, dst) {
		return false
	}
	switch (*dst).Reason {
	case ReasonNoToolCall, ReasonWrongTool, ReasonSchemaValidation, ReasonDeserialize:
		return true
	default:
		return false
	}
}

// structuredFeedback builds the turns that report a failed attempt back to the
// model. The tool path echoes the attempt's tool call and a tool_result error;
// the native path uses a plain assistant/user exchange.
func structuredFeedback(native bool, soErr *StructuredOutputError, rawText string, attempt int) []Message {
	msg := "The previous response did not produce valid structured output: " + soErr.Error() + ". Respond again, correcting the error."
	if native {
		return []Message{
			{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: rawText}}},
			{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: msg}}},
		}
	}
	// Providers reject duplicate tool-use IDs, so each failed attempt gets its own.
	callID := fmt.Sprintf("structured_output_retry_%d", attempt)
	var assistant ContentBlock = ToolUseBlock{ToolUseID: callID, Name: structuredOutputToolName, Input: json.RawMessage(rawText)}
	if !json.Valid([]byte(rawText)) {
		assistant = TextBlock{Text: rawText}
	}
	return []Message{
		{Role: RoleAssistant, Content: []ContentBlock{assistant}},
		{Role: RoleUser, Content: []ContentBlock{ToolResultBlock{ToolUseID: callID, Content: msg, IsError: true}}},
	}
}

// structuredAttempt runs one model call plus guardrails, schema validation and
// decode. On failure it returns the raw model text (for feedback) and the
// error; on success it returns the guardrail-processed JSON and metadata.
func (r *run) structuredAttempt(attempt int, system string, schema map[string]any, tools []tool.Spec, toolChoice *tool.Choice, native bool, cfg *InferenceConfig, baseMessages, feedback []Message, decode func([]byte) error) (string, map[string]any, error) {
	a, c, h := r.a, r.c, &r.h

	msgs := baseMessages
	if len(feedback) > 0 {
		msgs = append(append(make([]Message, 0, len(baseMessages)+len(feedback)), baseMessages...), feedback...)
	}
	modelReq := ModelRequest{
		Messages:        msgs,
		System:          system,
		Tools:           tools,
		ToolChoice:      toolChoice,
		InferenceConfig: cfg,
	}
	if native {
		modelReq.ResponseFormat = &ResponseFormat{Name: structuredOutputToolName, Schema: closedObjects(schema)}
	}

	provC, provF := h.onModelStart(c, ModelCallRecord{
		ModelID:         a.modelID(),
		Iteration:       attempt,
		System:          modelReq.System,
		MessageCount:    len(modelReq.Messages),
		InferenceConfig: modelReq.InferenceConfig,
	})
	resp, err := a.callProvider(provC, modelReq, nil)
	if err != nil {
		provF.finish(err, TokenUsage{}, 0, "")
		if errors.Is(err, ErrRateLimitExceeded) {
			h.onLimit(c, LimitRateLimit, 0, err)
		}
		return "", nil, &ProviderError{Cause: err}
	}
	provF.finishResponse(resp, "")
	cumulative := c.rt.addUsage(resp.Usage)
	if a.tokenBudget > 0 && cumulative.Total() > a.tokenBudget {
		h.onLimit(c, LimitTokenBudget, a.tokenBudget, ErrTokenBudgetExceeded)
		return "", nil, ErrTokenBudgetExceeded
	}

	var rawText string
	if native {
		rawText = resp.Text
	} else {
		if len(resp.ToolCalls) == 0 {
			return resp.Text, nil, &StructuredOutputError{Reason: ReasonNoToolCall}
		}
		var found *tool.Call
		for i := range resp.ToolCalls {
			if resp.ToolCalls[i].Name == structuredOutputToolName {
				found = &resp.ToolCalls[i]
				break
			}
		}
		if found == nil {
			return string(resp.ToolCalls[0].Input), nil, &StructuredOutputError{Reason: ReasonWrongTool}
		}
		rawText = string(found.Input)
	}
	for _, g := range a.outputGuardrails {
		gC, gf := h.onGuardrailStart(c, "output", rawText)
		rawText, err = g(gC, rawText)
		gf.finish(err, rawText)
		if err != nil {
			return rawText, nil, &GuardrailError{Direction: "output", Cause: err}
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
		return rawText, nil, &StructuredOutputError{Reason: ReasonDeserialize, Cause: fmt.Errorf("invalid JSON")}
	}
	if err := ValidateToolInput(schema, json.RawMessage(rawText)); err != nil {
		return rawText, nil, &StructuredOutputError{Reason: ReasonSchemaValidation, Cause: err}
	}
	if err := decode([]byte(rawText)); err != nil {
		return rawText, nil, &StructuredOutputError{Reason: ReasonDeserialize, Cause: err}
	}
	return rawText, resp.Metadata, nil
}

// closedObjects returns a copy of schema where every object with properties
// and no additionalProperties gets additionalProperties: false. Native schema
// modes require it, and decoding drops unknown fields anyway. Property names
// are never treated as schema keywords.
func closedObjects(schema map[string]any) map[string]any {
	out, _ := closeSchema(schema).(map[string]any)
	return out
}

// closeSchema copies one schema node. Keys of "properties" (and $defs-style
// maps) are names, so only their values are walked as schemas.
func closeSchema(v any) any {
	s, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(s)+1)
	for k, val := range s {
		switch k {
		case "properties", "$defs", "definitions", "patternProperties":
			if named, ok := val.(map[string]any); ok {
				walked := make(map[string]any, len(named))
				for name, sub := range named {
					walked[name] = closeSchema(sub)
				}
				out[k] = walked
				continue
			}
		case "items", "additionalProperties", "not":
			out[k] = closeSchema(val)
			continue
		case "anyOf", "oneOf", "allOf", "prefixItems":
			if list, ok := val.([]any); ok {
				walked := make([]any, len(list))
				for i, sub := range list {
					walked[i] = closeSchema(sub)
				}
				out[k] = walked
				continue
			}
		}
		out[k] = val
	}
	if _, set := s["additionalProperties"]; !set {
		if _, hasProps := s["properties"].(map[string]any); hasProps {
			out["additionalProperties"] = false
		}
	}
	return out
}
