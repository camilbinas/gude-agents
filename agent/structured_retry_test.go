package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// retryProvider returns queued responses in order and records every request.
// caps lets a test select the native or the schema-tool path.
type retryProvider struct {
	caps      ModelCapabilities
	responses []*ModelResponse
	calls     []ModelRequest
}

func (*retryProvider) Name() string                      { return "retry" }
func (p *retryProvider) Capabilities() ModelCapabilities { return p.caps }

func (p *retryProvider) Stream(_ context.Context, req ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	p.calls = append(p.calls, req)
	if len(p.calls) > len(p.responses) {
		return nil, errors.New("retryProvider: no more responses")
	}
	return p.responses[len(p.calls)-1], nil
}

func retryToolResp(raw string) *ModelResponse {
	return &ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "1", Name: structuredOutputToolName, Input: json.RawMessage(raw)}}}
}

func retryWrongToolResp() *ModelResponse {
	return &ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "1", Name: "other", Input: json.RawMessage(`{}`)}}}
}

func retryNoToolResp() *ModelResponse { return &ModelResponse{Text: "plain text answer"} }

var retrySchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"name": map[string]any{"type": "string"}},
	"required":   []any{"name"},
}

// --- tool path ---

func TestStructuredRetry_ToolPath_SchemaValidationThenSuccess(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{
		retryToolResp(`{"other":"x"}`), // fails schema validation (missing required name)
		retryToolResp(`{"name":"gude"}`),
	}}
	a, err := New(p, "sys") // default retries = 1
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Name string }
	res, err := a.InvokeSchema(Background(), "q", retrySchema, func(raw json.RawMessage) error { return json.Unmarshal(raw, &out) })
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if len(p.calls) != 2 {
		t.Fatalf("want 2 model calls, got %d", len(p.calls))
	}
	if out.Name != "gude" || res.Text != `{"name":"gude"}` {
		t.Fatalf("decoded %+v, text %q", out, res.Text)
	}
	// The retry must feed the failure back as a tool_result error.
	second := p.calls[1].Messages
	last := second[len(second)-1]
	tr, ok := last.Content[0].(ToolResultBlock)
	if last.Role != RoleUser || !ok || !tr.IsError {
		t.Fatalf("retry must append a tool_result error, got %+v", last)
	}
	prev := second[len(second)-2]
	if prev.Role != RoleAssistant {
		t.Fatalf("retry must echo the failed assistant turn, got %+v", prev)
	}
	if _, ok := prev.Content[0].(ToolUseBlock); !ok {
		t.Fatalf("failed tool-call attempt must be echoed as a ToolUseBlock, got %T", prev.Content[0])
	}
}

func TestStructuredRetry_ToolPath_NoToolCallThenSuccess(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{retryNoToolResp(), retryToolResp(`{"name":"ok"}`)}}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.InvokeSchema(Background(), "q", retrySchema, func(json.RawMessage) error { return nil }); err != nil {
		t.Fatalf("want success after no_tool_call retry, got %v", err)
	}
	if len(p.calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(p.calls))
	}
}

func TestStructuredRetry_ToolPath_WrongToolThenSuccess(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{retryWrongToolResp(), retryToolResp(`{"name":"ok"}`)}}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.InvokeSchema(Background(), "q", retrySchema, func(json.RawMessage) error { return nil }); err != nil {
		t.Fatalf("want success after wrong_tool retry, got %v", err)
	}
	if len(p.calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(p.calls))
	}
}

func TestStructuredRetry_InvalidJSONThenSuccess(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{retryToolResp(`{not json`), retryToolResp(`{"name":"ok"}`)}}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.InvokeSchema(Background(), "q", retrySchema, func(json.RawMessage) error { return nil }); err != nil {
		t.Fatalf("want success after deserialize retry, got %v", err)
	}
	if len(p.calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(p.calls))
	}
	// Invalid JSON cannot be echoed as a tool call; feedback uses a TextBlock.
	prev := p.calls[1].Messages[len(p.calls[1].Messages)-2]
	if _, ok := prev.Content[0].(TextBlock); !ok {
		t.Fatalf("invalid-JSON attempt must be echoed as text, got %T", prev.Content[0])
	}
}

func TestStructuredRetry_ExhaustedKeepsFinalReason(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{retryToolResp(`{"other":1}`), retryToolResp(`{"other":2}`)}}
	a, err := New(p, "sys") // 1 retry => 2 attempts, both fail
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.InvokeSchema(Background(), "q", retrySchema, func(json.RawMessage) error { return nil })
	var se *StructuredOutputError
	if !errors.As(err, &se) || se.Reason != ReasonSchemaValidation {
		t.Fatalf("want final ReasonSchemaValidation, got %v", err)
	}
	if len(p.calls) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(p.calls))
	}
}

func TestStructuredRetry_DisabledMakesOneAttempt(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{retryToolResp(`{"other":1}`), retryToolResp(`{"name":"ok"}`)}}
	a, err := New(p, "sys", WithStructuredOutputRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.InvokeSchema(Background(), "q", retrySchema, func(json.RawMessage) error { return nil })
	var se *StructuredOutputError
	if !errors.As(err, &se) || se.Reason != ReasonSchemaValidation {
		t.Fatalf("want ReasonSchemaValidation with retries disabled, got %v", err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("retries disabled: want 1 call, got %d", len(p.calls))
	}
}

func TestStructuredRetry_ConfigurableCount(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{
		retryToolResp(`{"a":1}`), retryToolResp(`{"b":2}`), retryToolResp(`{"name":"ok"}`),
	}}
	a, err := New(p, "sys", WithStructuredOutputRetries(2)) // 3 attempts
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.InvokeSchema(Background(), "q", retrySchema, func(json.RawMessage) error { return nil }); err != nil {
		t.Fatalf("want success on third attempt, got %v", err)
	}
	if len(p.calls) != 3 {
		t.Fatalf("want 3 attempts, got %d", len(p.calls))
	}
}

// --- native path ---

func TestStructuredRetry_NativePath_FeedbackIsUserTurn(t *testing.T) {
	p := &retryProvider{
		caps:      ModelCapabilities{NativeStructuredOutput: Supported},
		responses: []*ModelResponse{{Text: `{"other":1}`}, {Text: `{"name":"gude"}`}},
	}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Name string }
	if _, err := a.InvokeSchema(Background(), "q", retrySchema, func(raw json.RawMessage) error { return json.Unmarshal(raw, &out) }); err != nil {
		t.Fatalf("native retry failed: %v", err)
	}
	if len(p.calls) != 2 || out.Name != "gude" {
		t.Fatalf("calls=%d out=%+v", len(p.calls), out)
	}
	if p.calls[1].ResponseFormat == nil {
		t.Fatal("native retry must keep sending ResponseFormat")
	}
	second := p.calls[1].Messages
	last := second[len(second)-1]
	if last.Role != RoleUser {
		t.Fatalf("native feedback must be a user turn, got %+v", last)
	}
	if _, ok := last.Content[0].(ToolResultBlock); ok {
		t.Fatal("native feedback must not use a tool_result block")
	}
	prev := second[len(second)-2]
	if prev.Role != RoleAssistant {
		t.Fatalf("native path must echo the failed assistant text, got %+v", prev)
	}
}

// --- usage + persistence ---

func TestStructuredRetry_CountsUsageOfEveryAttempt(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{Name: structuredOutputToolName, Input: json.RawMessage(`{"other":1}`)}}, Usage: TokenUsage{InputTokens: 10, OutputTokens: 5}},
		{ToolCalls: []tool.Call{{Name: structuredOutputToolName, Input: json.RawMessage(`{"name":"ok"}`)}}, Usage: TokenUsage{InputTokens: 7, OutputTokens: 3}},
	}}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.InvokeSchema(Background(), "q", retrySchema, func(json.RawMessage) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Usage.Total(); got != 25 {
		t.Fatalf("want total usage 25 across both attempts, got %d", got)
	}
}

func TestStructuredRetry_TokenBudgetStopsMidRetry(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{Name: structuredOutputToolName, Input: json.RawMessage(`{"other":1}`)}}, Usage: TokenUsage{InputTokens: 100}},
		{ToolCalls: []tool.Call{{Name: structuredOutputToolName, Input: json.RawMessage(`{"name":"ok"}`)}}, Usage: TokenUsage{InputTokens: 100}},
	}}
	a, err := New(p, "sys", WithTokenBudget(150))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.InvokeSchema(Background(), "q", retrySchema, func(json.RawMessage) error { return nil })
	if !errors.Is(err, ErrTokenBudgetExceeded) {
		t.Fatalf("want ErrTokenBudgetExceeded on the retry, got %v", err)
	}
	if len(p.calls) != 2 {
		t.Fatalf("budget should trip on the second attempt, got %d calls", len(p.calls))
	}
}

func TestStructuredRetry_PersistsOnlyFinalSuccess(t *testing.T) {
	p := &retryProvider{responses: []*ModelResponse{retryToolResp(`{"other":1}`), retryToolResp(`{"name":"ok"}`)}}
	store := newTestMemoryStore()
	a, err := New(p, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("c1")
	if _, err := a.InvokeSchema(ctx, "q", retrySchema, func(json.RawMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	msgs := snap.Messages
	// Only the user question and the final successful answer are persisted.
	for _, m := range msgs {
		for _, b := range m.Content {
			if tb, ok := b.(TextBlock); ok && tb.Text == `{"other":1}` {
				t.Fatal("failed attempt must not be persisted")
			}
		}
	}
	var sawFinal bool
	for _, m := range msgs {
		for _, b := range m.Content {
			if tb, ok := b.(TextBlock); ok && tb.Text == `{"name":"ok"}` {
				sawFinal = true
			}
		}
	}
	if !sawFinal {
		t.Fatal("final successful answer must be persisted")
	}
}
