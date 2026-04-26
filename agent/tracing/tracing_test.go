package tracing

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestTracerProvider() (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	return exp, tp
}

func findSpan(spans []tracetest.SpanStub, name string) *tracetest.SpanStub {
	for i := range spans {
		if spans[i].Name == name {
			return &spans[i]
		}
	}
	return nil
}

func findSpans(spans []tracetest.SpanStub, name string) []tracetest.SpanStub {
	var result []tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == name {
			result = append(result, spans[i])
		}
	}
	return result
}

func getAttr(span tracetest.SpanStub, key string) attribute.Value {
	for _, a := range span.Attributes {
		if string(a.Key) == key {
			return a.Value
		}
	}
	return attribute.Value{}
}

func hasEvent(span tracetest.SpanStub, name string) bool {
	for _, e := range span.Events {
		if e.Name == name {
			return true
		}
	}
	return false
}

func getEventAttr(span tracetest.SpanStub, eventName, key string) attribute.Value {
	for _, e := range span.Events {
		if e.Name == eventName {
			for _, a := range e.Attributes {
				if string(a.Key) == key {
					return a.Value
				}
			}
		}
	}
	return attribute.Value{}
}

// Suppress unused import warnings.
var (
	_ = otel.GetTracerProvider
	_ = codes.Ok
	_ = trace.SpanFromContext
)

// ---------------------------------------------------------------------------
// Mock Provider
// ---------------------------------------------------------------------------

func newMockProvider(responses ...*agent.ProviderResponse) *testutil.MockProvider {
	return testutil.NewMockProvider(testutil.WithResponses(responses...))
}

func newMockProviderWithModel(modelID string, responses ...*agent.ProviderResponse) *testutil.MockProvider {
	return testutil.NewMockProvider(testutil.WithModelID(modelID), testutil.WithResponses(responses...))
}

type errorProvider struct{ err error }

func (ep *errorProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	return nil, ep.err
}
func (ep *errorProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, _ agent.StreamCallback) (*agent.ProviderResponse, error) {
	return nil, ep.err
}

// ---------------------------------------------------------------------------
// Mock Memory
// ---------------------------------------------------------------------------

type mockMemory struct {
	mu      sync.RWMutex
	data    map[string][]agent.Message
	loadErr error
	saveErr error
}

func newMockMemory() *mockMemory {
	return &mockMemory{data: make(map[string][]agent.Message)}
}

func (m *mockMemory) Load(_ context.Context, id string) ([]agent.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.data[id], nil
}

func (m *mockMemory) Save(_ context.Context, id string, msgs []agent.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	cp := make([]agent.Message, len(msgs))
	copy(cp, msgs)
	m.data[id] = cp
	return nil
}

// ---------------------------------------------------------------------------
// Mock Retriever
// ---------------------------------------------------------------------------

type mockRetriever struct {
	docs []agent.Document
	err  error
}

func (r *mockRetriever) Retrieve(_ context.Context, _ string) ([]agent.Document, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.docs, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func tc(id, name string) tool.Call {
	return tool.Call{ToolUseID: id, Name: name, Input: json.RawMessage(`{}`)}
}

func dummyTool(name, desc string) tool.Tool {
	return tool.NewRaw(name, desc, map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "ok", nil
		})
}

// ===========================================================================
// ===========================================================================

// 7.1 WithTracing option wiring

func TestWithTracing_NonNilProvider_SetsHook(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()
	if findSpan(spans, "agent.invoke") == nil {
		t.Fatal("expected agent.invoke span when WithTracing is provided with a non-nil TracerProvider")
	}
}

func TestWithTracing_NilProvider_UsesGlobalProvider(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(nil)

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()
	if findSpan(spans, "agent.invoke") == nil {
		t.Fatal("expected agent.invoke span when WithTracing(nil) uses global TracerProvider")
	}
}

func TestWithoutTracing_NoSpansCreated(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()
	if len(spans) != 0 {
		t.Errorf("expected 0 spans without WithTracing, got %d", len(spans))
	}
}

// 7.2 Zero-overhead when tracing is not enabled

func TestZeroOverhead_NoTracingNoSpans(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	mem := newMockMemory()
	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{tc("tc1", "echo")}},
		&agent.ProviderResponse{Text: "done"},
	)
	echoTool := tool.NewRaw("echo", "echoes", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) { return "echoed", nil })

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{echoTool},
		agent.WithConversation(mem, "conv-1"),
		agent.WithInputGuardrail(func(_ context.Context, msg string) (string, error) { return msg, nil }),
		agent.WithOutputGuardrail(func(_ context.Context, resp string) (string, error) { return resp, nil }),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()
	if len(spans) != 0 {
		t.Errorf("expected 0 spans without WithTracing (zero-overhead), got %d", len(spans))
	}
}

// 7.3 Structured logger tests

func TestStructuredLogger_TraceIDAndSpanID_WhenSpanActive(t *testing.T) {
	_, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	var captured map[string]any
	logger := NewLogger(ctx)
	logger.Output = func(_ context.Context, _ string, fields map[string]any) {
		captured = fields
	}

	logger.Printf("hello %s", "world")

	if captured == nil {
		t.Fatal("expected output to be called")
	}
	sc := span.SpanContext()
	if captured["trace_id"] != sc.TraceID().String() {
		t.Errorf("trace_id mismatch: expected %s, got %s", sc.TraceID(), captured["trace_id"])
	}
	if captured["span_id"] != sc.SpanID().String() {
		t.Errorf("span_id mismatch: expected %s, got %s", sc.SpanID(), captured["span_id"])
	}
	if captured["msg"] != "hello world" {
		t.Errorf("expected msg=%q, got %q", "hello world", captured["msg"])
	}
}

func TestStructuredLogger_NoTraceFields_WhenNoSpan(t *testing.T) {
	var captured map[string]any
	logger := NewLogger(context.Background())
	logger.Output = func(_ context.Context, _ string, fields map[string]any) {
		captured = fields
	}

	logger.Printf("no span")

	if captured == nil {
		t.Fatal("expected output to be called")
	}
	if _, ok := captured["trace_id"]; ok {
		t.Error("expected no trace_id field when no span is active")
	}
	if _, ok := captured["span_id"]; ok {
		t.Error("expected no span_id field when no span is active")
	}
}

func TestStructuredLogger_StructuredOutputFormat(t *testing.T) {
	_, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	var captured map[string]any
	logger := NewLogger(ctx)
	logger.Output = func(_ context.Context, _ string, fields map[string]any) {
		captured = fields
	}

	logger.Printf("test message %d", 42)

	if captured["msg"] != "test message 42" {
		t.Errorf("expected msg=%q, got %q", "test message 42", captured["msg"])
	}
	if len(captured) < 3 {
		t.Errorf("expected at least 3 fields (msg, trace_id, span_id), got %d", len(captured))
	}
}

func TestWithTracing_AutoLoggerSelection(t *testing.T) {
	_, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ===========================================================================
// ===========================================================================

// 8.1 Agent invocation span

func TestInvokeSpan_Created(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	invokeSpan := findSpan(spans, "agent.invoke")
	if invokeSpan == nil {
		t.Fatal("expected agent.invoke span")
	}
}

func TestInvokeSpan_Attributes(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	mem := newMockMemory()
	prov := newMockProviderWithModel("test-model-v1",
		&agent.ProviderResponse{Text: "hello", Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	)

	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithMaxIterations(7),
		agent.WithConversation(mem, "conv-123"),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	invokeSpan := findSpan(spans, "agent.invoke")
	if invokeSpan == nil {
		t.Fatal("expected agent.invoke span")
	}

	if v := getAttr(*invokeSpan, AttrAgentMaxIterations); v.AsInt64() != 7 {
		t.Errorf("expected agent.max_iterations=7, got %d", v.AsInt64())
	}
	if v := getAttr(*invokeSpan, AttrAgentModelID); v.AsString() != "test-model-v1" {
		t.Errorf("expected agent.model_id=%q, got %q", "test-model-v1", v.AsString())
	}
	if v := getAttr(*invokeSpan, AttrAgentConversationID); v.AsString() != "conv-123" {
		t.Errorf("expected agent.conversation_id=%q, got %q", "conv-123", v.AsString())
	}
	if v := getAttr(*invokeSpan, AttrGenAISystem); v.AsString() != "gude-agents" {
		t.Errorf("expected gen_ai.system=%q, got %q", "gude-agents", v.AsString())
	}
}

func TestInvokeSpan_OKStatusOnSuccess(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{
		Text:  "hello",
		Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
	})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	invokeSpan := findSpan(spans, "agent.invoke")
	if invokeSpan == nil {
		t.Fatal("expected agent.invoke span")
	}

	if invokeSpan.Status.Code != codes.Ok {
		t.Errorf("expected OK status, got %v", invokeSpan.Status.Code)
	}

	if v := getAttr(*invokeSpan, AttrAgentTokenUsageInput); v.AsInt64() != 10 {
		t.Errorf("expected token_usage.input=10, got %d", v.AsInt64())
	}
	if v := getAttr(*invokeSpan, AttrAgentTokenUsageOutput); v.AsInt64() != 5 {
		t.Errorf("expected token_usage.output=5, got %d", v.AsInt64())
	}
}

func TestInvokeSpan_ErrorStatusOnFailure(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := &errorProvider{err: fmt.Errorf("connection refused")}
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error")
	}

	spans := exp.GetSpans()
	invokeSpan := findSpan(spans, "agent.invoke")
	if invokeSpan == nil {
		t.Fatal("expected agent.invoke span")
	}

	if invokeSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status, got %v", invokeSpan.Status.Code)
	}
}

// 8.2 Iteration spans

func TestIterationSpans_Created(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	// Two iterations: first returns tool call, second returns text.
	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{tc("tc1", "echo")}},
		&agent.ProviderResponse{Text: "done"},
	)
	echoTool := dummyTool("echo", "echoes")

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{echoTool}, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	iterSpans := findSpans(spans, "agent.iteration")
	if len(iterSpans) != 2 {
		t.Fatalf("expected 2 iteration spans, got %d", len(iterSpans))
	}

	// Verify iteration numbers are 1-based.
	for i, s := range iterSpans {
		v := getAttr(s, AttrAgentIterationNumber)
		if v.AsInt64() != int64(i+1) {
			t.Errorf("iteration[%d]: expected number=%d, got %d", i, i+1, v.AsInt64())
		}
	}
}

func TestIterationSpans_ToolCount(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{tc("tc1", "a"), tc("tc2", "b")}},
		&agent.ProviderResponse{Text: "done"},
	)

	a, err := agent.New(prov, prompt.Text("sys"),
		[]tool.Tool{dummyTool("a", "tool a"), dummyTool("b", "tool b")},
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	iterSpans := findSpans(spans, "agent.iteration")
	if len(iterSpans) < 1 {
		t.Fatal("expected at least 1 iteration span")
	}

	// First iteration should have tool_count=2.
	v := getAttr(iterSpans[0], AttrAgentIterationToolCount)
	if v.AsInt64() != 2 {
		t.Errorf("expected iteration.tool_count=2, got %d", v.AsInt64())
	}
}

func TestIterationSpans_FinalTrue(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{tc("tc1", "echo")}},
		&agent.ProviderResponse{Text: "done"},
	)
	echoTool := dummyTool("echo", "echoes")

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{echoTool}, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	iterSpans := findSpans(spans, "agent.iteration")
	if len(iterSpans) != 2 {
		t.Fatalf("expected 2 iteration spans, got %d", len(iterSpans))
	}

	// First iteration: final=false (has tool calls).
	v1 := getAttr(iterSpans[0], AttrAgentIterationFinal)
	if v1.AsBool() {
		t.Error("expected first iteration final=false")
	}

	// Last iteration: final=true (text response).
	v2 := getAttr(iterSpans[1], AttrAgentIterationFinal)
	if !v2.AsBool() {
		t.Error("expected last iteration final=true")
	}
}

func TestIterationSpans_ParentIsInvoke(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	invokeSpan := findSpan(spans, "agent.invoke")
	iterSpans := findSpans(spans, "agent.iteration")
	if invokeSpan == nil || len(iterSpans) == 0 {
		t.Fatal("expected both invoke and iteration spans")
	}

	for _, iter := range iterSpans {
		if iter.Parent.SpanID() != invokeSpan.SpanContext.SpanID() {
			t.Errorf("iteration span parent should be invoke span")
		}
	}
}

// 8.3 Provider call spans

func TestProviderCallSpan_Created(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{
		Text:  "hello",
		Usage: agent.TokenUsage{InputTokens: 100, OutputTokens: 50},
	})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	provSpan := findSpan(spans, "agent.provider.call")
	if provSpan == nil {
		t.Fatal("expected agent.provider.call span")
	}

	if v := getAttr(*provSpan, AttrProviderInputTokens); v.AsInt64() != 100 {
		t.Errorf("expected provider.input_tokens=100, got %d", v.AsInt64())
	}
	if v := getAttr(*provSpan, AttrProviderOutputTokens); v.AsInt64() != 50 {
		t.Errorf("expected provider.output_tokens=50, got %d", v.AsInt64())
	}
}

func TestProviderCallSpan_ToolCallCount(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(
		&agent.ProviderResponse{
			ToolCalls: []tool.Call{tc("tc1", "a"), tc("tc2", "b"), tc("tc3", "c")},
			Usage:     agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
		&agent.ProviderResponse{Text: "done"},
	)

	a, err := agent.New(prov, prompt.Text("sys"),
		[]tool.Tool{dummyTool("a", "a"), dummyTool("b", "b"), dummyTool("c", "c")},
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	provSpans := findSpans(spans, "agent.provider.call")
	if len(provSpans) < 1 {
		t.Fatal("expected at least 1 provider call span")
	}

	// First provider call returned 3 tool calls.
	v := getAttr(provSpans[0], AttrProviderToolCalls)
	if v.AsInt64() != 3 {
		t.Errorf("expected provider.tool_calls=3, got %d", v.AsInt64())
	}
}

func TestProviderCallSpan_ErrorStatus(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := &errorProvider{err: fmt.Errorf("timeout")}
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, _ = a.Invoke(context.Background(), "hi")

	spans := exp.GetSpans()
	provSpan := findSpan(spans, "agent.provider.call")
	if provSpan == nil {
		t.Fatal("expected agent.provider.call span")
	}

	if provSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on provider call span, got %v", provSpan.Status.Code)
	}
}

// 8.4 Tool execution spans

func TestToolSpan_NameAndAttribute(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{tc("tc1", "search")}},
		&agent.ProviderResponse{Text: "done"},
	)
	searchTool := dummyTool("search", "search things")

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{searchTool}, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	toolSpan := findSpan(spans, "agent.tool.search")
	if toolSpan == nil {
		t.Fatal("expected agent.tool.search span")
	}

	if v := getAttr(*toolSpan, AttrToolName); v.AsString() != "search" {
		t.Errorf("expected tool.name=%q, got %q", "search", v.AsString())
	}
}

func TestToolSpan_ErrorOnValidationFailure(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	// Tool with required field — provider sends empty input.
	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{tc("tc1", "strict")}},
		&agent.ProviderResponse{Text: "done"},
	)
	strictTool := tool.NewRaw("strict", "strict tool", map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
		"required":   []any{"name"},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "ok", nil
	})

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{strictTool}, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	toolSpan := findSpan(spans, "agent.tool.strict")
	if toolSpan == nil {
		t.Fatal("expected agent.tool.strict span")
	}

	if toolSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on tool span for validation failure, got %v", toolSpan.Status.Code)
	}
}

func TestToolSpan_ErrorOnHandlerError(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{tc("tc1", "fail")}},
		&agent.ProviderResponse{Text: "recovered"},
	)
	failTool := tool.NewRaw("fail", "always fails", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("tool exploded")
		})

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{failTool}, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	toolSpan := findSpan(spans, "agent.tool.fail")
	if toolSpan == nil {
		t.Fatal("expected agent.tool.fail span")
	}

	if toolSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on tool span for handler error, got %v", toolSpan.Status.Code)
	}
}

func TestToolSpan_ParallelToolsShareParent(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{
			tc("tc1", "alpha"),
			tc("tc2", "beta"),
			tc("tc3", "gamma"),
		}},
		&agent.ProviderResponse{Text: "done"},
	)

	a, err := agent.New(prov, prompt.Text("sys"),
		[]tool.Tool{dummyTool("alpha", "a"), dummyTool("beta", "b"), dummyTool("gamma", "g")},
		agent.WithParallelToolExecution(),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	iterSpans := findSpans(spans, "agent.iteration")
	if len(iterSpans) < 1 {
		t.Fatal("expected at least 1 iteration span")
	}
	iterSpanID := iterSpans[0].SpanContext.SpanID()

	// All three tool spans should share the same parent (the iteration span).
	toolNames := []string{"agent.tool.alpha", "agent.tool.beta", "agent.tool.gamma"}
	for _, name := range toolNames {
		s := findSpan(spans, name)
		if s == nil {
			t.Fatalf("expected %s span", name)
		}
		if s.Parent.SpanID() != iterSpanID {
			t.Errorf("%s parent should be iteration span", name)
		}
	}
}

// 8.5 Guardrail spans

func TestGuardrailSpan_Input(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithInputGuardrail(func(_ context.Context, msg string) (string, error) {
			return msg, nil
		}),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	gSpan := findSpan(spans, "agent.guardrail.input")
	if gSpan == nil {
		t.Fatal("expected agent.guardrail.input span")
	}
}

func TestGuardrailSpan_Output(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithOutputGuardrail(func(_ context.Context, resp string) (string, error) {
			return resp, nil
		}),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	gSpan := findSpan(spans, "agent.guardrail.output")
	if gSpan == nil {
		t.Fatal("expected agent.guardrail.output span")
	}
}

func TestGuardrailSpan_InputErrorStatus(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithInputGuardrail(func(_ context.Context, msg string) (string, error) {
			return "", fmt.Errorf("blocked")
		}),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, _ = a.Invoke(context.Background(), "hi")

	spans := exp.GetSpans()
	gSpan := findSpan(spans, "agent.guardrail.input")
	if gSpan == nil {
		t.Fatal("expected agent.guardrail.input span")
	}

	if gSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on input guardrail span, got %v", gSpan.Status.Code)
	}
}

func TestGuardrailSpan_OutputErrorStatus(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithOutputGuardrail(func(_ context.Context, resp string) (string, error) {
			return "", fmt.Errorf("output blocked")
		}),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, _ = a.Invoke(context.Background(), "hi")

	spans := exp.GetSpans()
	gSpan := findSpan(spans, "agent.guardrail.output")
	if gSpan == nil {
		t.Fatal("expected agent.guardrail.output span")
	}

	if gSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on output guardrail span, got %v", gSpan.Status.Code)
	}
}

// 8.6 Memory spans

func TestMemorySpan_LoadAndSave(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	mem := newMockMemory()
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithConversation(mem, "conv-42"),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()

	loadSpan := findSpan(spans, "agent.conversation.load")
	if loadSpan == nil {
		t.Fatal("expected agent.conversation.load span")
	}
	if v := getAttr(*loadSpan, AttrMemoryConversationID); v.AsString() != "conv-42" {
		t.Errorf("expected memory.conversation_id=%q, got %q", "conv-42", v.AsString())
	}

	saveSpan := findSpan(spans, "agent.conversation.save")
	if saveSpan == nil {
		t.Fatal("expected agent.conversation.save span")
	}
	if v := getAttr(*saveSpan, AttrMemoryConversationID); v.AsString() != "conv-42" {
		t.Errorf("expected memory.conversation_id=%q, got %q", "conv-42", v.AsString())
	}
}

func TestMemorySpan_LoadErrorStatus(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	mem := &mockMemory{
		data:    make(map[string][]agent.Message),
		loadErr: fmt.Errorf("disk on fire"),
	}
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithConversation(mem, "conv-1"),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, _ = a.Invoke(context.Background(), "hi")

	spans := exp.GetSpans()
	loadSpan := findSpan(spans, "agent.conversation.load")
	if loadSpan == nil {
		t.Fatal("expected agent.conversation.load span")
	}

	if loadSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on memory load span, got %v", loadSpan.Status.Code)
	}
}

func TestMemorySpan_SaveErrorStatus(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	mem := &mockMemory{
		data:    make(map[string][]agent.Message),
		saveErr: fmt.Errorf("write failed"),
	}
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithConversation(mem, "conv-1"),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, invokeErr := a.Invoke(context.Background(), "hi")
	if invokeErr == nil {
		t.Fatal("expected error from memory save failure")
	}

	spans := exp.GetSpans()
	saveSpan := findSpan(spans, "agent.conversation.save")
	if saveSpan == nil {
		t.Fatal("expected agent.conversation.save span")
	}

	if saveSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on memory save span, got %v", saveSpan.Status.Code)
	}
}

// 8.7 Retriever spans

func TestRetrieverSpan_Created(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	ret := &mockRetriever{docs: []agent.Document{
		{Content: "doc1"},
		{Content: "doc2"},
		{Content: "doc3"},
	}}
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithRetriever(ret),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	retSpan := findSpan(spans, "agent.retriever.retrieve")
	if retSpan == nil {
		t.Fatal("expected agent.retriever.retrieve span")
	}

	if v := getAttr(*retSpan, AttrRetrieverDocumentCount); v.AsInt64() != 3 {
		t.Errorf("expected retriever.document_count=3, got %d", v.AsInt64())
	}
}

func TestRetrieverSpan_ErrorStatus(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	ret := &mockRetriever{err: fmt.Errorf("retriever timeout")}
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithRetriever(ret),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, _ = a.Invoke(context.Background(), "hi")

	spans := exp.GetSpans()
	retSpan := findSpan(spans, "agent.retriever.retrieve")
	if retSpan == nil {
		t.Fatal("expected agent.retriever.retrieve span")
	}

	if retSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on retriever span, got %v", retSpan.Status.Code)
	}
}

// 8.8 Max iterations event

func TestMaxIterationsExceeded_EventRecorded(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	// Provider always returns tool calls — never a final answer.
	alwaysToolCall := &agent.ProviderResponse{ToolCalls: []tool.Call{tc("tc", "loop")}}
	prov := newMockProvider(alwaysToolCall, alwaysToolCall, alwaysToolCall)

	loopTool := dummyTool("loop", "loops forever")

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{loopTool},
		agent.WithMaxIterations(2),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, invokeErr := a.Invoke(context.Background(), "loop")
	if invokeErr == nil {
		t.Fatal("expected max iteration error")
	}

	spans := exp.GetSpans()
	invokeSpan := findSpan(spans, "agent.invoke")
	if invokeSpan == nil {
		t.Fatal("expected agent.invoke span")
	}

	if !hasEvent(*invokeSpan, EventMaxIterationsExceeded) {
		t.Error("expected agent.max_iterations_exceeded event on invoke span")
	}

	// Verify the event carries the max_iterations attribute.
	v := getEventAttr(*invokeSpan, EventMaxIterationsExceeded, AttrAgentMaxIterations)
	if v.AsInt64() != 2 {
		t.Errorf("expected max_iterations=2 in event, got %d", v.AsInt64())
	}

	// The invoke span should also have Error status.
	if invokeSpan.Status.Code != codes.Error {
		t.Errorf("expected Error status on invoke span after max iterations, got %v", invokeSpan.Status.Code)
	}
}

// ===========================================================================
// ===========================================================================

func TestMultiAgentComposition_ChildSpansUnderParentToolSpan(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	// Child agent: responds with a simple text.
	childProvider := newMockProvider(&agent.ProviderResponse{
		Text:  "child response",
		Usage: agent.TokenUsage{InputTokens: 5, OutputTokens: 3},
	})
	childAgent, err := agent.New(childProvider, prompt.Text("child instructions"), nil, WithTracing(tp))
	if err != nil {
		t.Fatalf("creating child agent: %v", err)
	}

	// Parent agent: first response triggers the child agent tool, second is final answer.
	childTool := agent.AgentAsTool("child_agent", "delegates to child", childAgent)
	parentProvider := newMockProvider(
		&agent.ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc1", Name: "child_agent", Input: json.RawMessage(`{"message":"hello child"}`)},
			},
			Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 8},
		},
		&agent.ProviderResponse{
			Text:  "parent done",
			Usage: agent.TokenUsage{InputTokens: 15, OutputTokens: 12},
		},
	)
	parentAgent, err := agent.New(parentProvider, prompt.Text("parent instructions"),
		[]tool.Tool{childTool}, WithTracing(tp))
	if err != nil {
		t.Fatalf("creating parent agent: %v", err)
	}

	_, _, err = parentAgent.Invoke(context.Background(), "delegate to child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exp.GetSpans()

	// Find the parent's tool span for the child agent.
	parentToolSpan := findSpan(spans, "agent.tool.child_agent")
	if parentToolSpan == nil {
		t.Fatal("expected agent.tool.child_agent span from parent agent")
	}

	// Find the child's invoke span. There should be two agent.invoke spans:
	// one for the parent and one for the child.
	invokeSpans := findSpans(spans, "agent.invoke")
	if len(invokeSpans) < 2 {
		t.Fatalf("expected at least 2 agent.invoke spans (parent + child), got %d", len(invokeSpans))
	}

	// The child's agent.invoke span should be a child of the parent's agent.tool.child_agent span.
	// Find the invoke span whose parent is the tool span.
	var childInvokeSpan *tracetest.SpanStub
	for i := range invokeSpans {
		if invokeSpans[i].Parent.SpanID() == parentToolSpan.SpanContext.SpanID() {
			childInvokeSpan = &invokeSpans[i]
			break
		}
	}
	if childInvokeSpan == nil {
		t.Fatal("expected child agent.invoke span to be a child of parent's agent.tool.child_agent span")
	}

	// Verify they share the same trace ID (distributed trace propagation).
	if childInvokeSpan.SpanContext.TraceID() != parentToolSpan.SpanContext.TraceID() {
		t.Error("child invoke span should share the same trace ID as parent tool span")
	}
}

// ===========================================================================
// ===========================================================================

func TestMiddleware_ContextPropagation_ChildSpanUnderToolSpan(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("test-middleware")

	// Middleware that extracts the span from context and creates a child span.
	mw := func(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
		return func(ctx context.Context, toolName string, input json.RawMessage) (string, error) {
			// Extract the active span from context and create a child span.
			_, childSpan := tracer.Start(ctx, "middleware.custom")
			defer childSpan.End()

			return next(ctx, toolName, input)
		}
	}

	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{tc("tc1", "my_tool")}},
		&agent.ProviderResponse{Text: "done"},
	)
	myTool := dummyTool("my_tool", "a tool")

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{myTool},
		agent.WithMiddleware(mw),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()

	// Find the tool span.
	toolSpan := findSpan(spans, "agent.tool.my_tool")
	if toolSpan == nil {
		t.Fatal("expected agent.tool.my_tool span")
	}

	// Find the middleware-created child span.
	mwSpan := findSpan(spans, "middleware.custom")
	if mwSpan == nil {
		t.Fatal("expected middleware.custom span")
	}

	// The middleware span should be a child of the tool span.
	if mwSpan.Parent.SpanID() != toolSpan.SpanContext.SpanID() {
		t.Errorf("middleware.custom span parent should be agent.tool.my_tool span, got parent span ID %s",
			mwSpan.Parent.SpanID())
	}

	// They should share the same trace ID.
	if mwSpan.SpanContext.TraceID() != toolSpan.SpanContext.TraceID() {
		t.Error("middleware span should share the same trace ID as tool span")
	}
}

func TestParallelToolExecution_WithOtelTracing(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{
			tc("tc1", "a"),
			tc("tc2", "b"),
			tc("tc3", "c"),
		}},
		&agent.ProviderResponse{Text: "parallel done"},
	)

	const toolSleep = 100 * time.Millisecond

	var barrier sync.WaitGroup
	barrier.Add(3)

	makeTool := func(name string) tool.Tool {
		return tool.NewRaw(name, name+" tool", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				barrier.Done()
				barrier.Wait()
				time.Sleep(toolSleep)
				return name + " ok", nil
			})
	}

	a, err := agent.New(prov, prompt.Text("sys"),
		[]tool.Tool{makeTool("a"), makeTool("b"), makeTool("c")},
		agent.WithParallelToolExecution(),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	result, _, err := a.Invoke(context.Background(), "go parallel")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "parallel done" {
		t.Errorf("expected %q, got %q", "parallel done", result)
	}

	// If tools ran in parallel, total time should be ~1x toolSleep.
	// If sequential, it would be ~3x toolSleep (300ms) — or deadlock on the barrier.
	if elapsed >= 2*toolSleep {
		t.Errorf("tools ran sequentially with OTEL tracing: elapsed %v, expected < %v", elapsed, 2*toolSleep)
	}

	// Verify all 3 tool spans were created.
	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	toolSpans := findSpans(spans, "agent.tool.a")
	toolSpans = append(toolSpans, findSpans(spans, "agent.tool.b")...)
	toolSpans = append(toolSpans, findSpans(spans, "agent.tool.c")...)
	if len(toolSpans) != 3 {
		t.Errorf("expected 3 tool spans, got %d", len(toolSpans))
	}

	// Verify spans overlapped (parallel execution).
	if len(toolSpans) == 3 {
		// All spans should have started before any span ended.
		latestStart := toolSpans[0].StartTime
		earliestEnd := toolSpans[0].EndTime
		for _, s := range toolSpans[1:] {
			if s.StartTime.After(latestStart) {
				latestStart = s.StartTime
			}
			if s.EndTime.Before(earliestEnd) {
				earliestEnd = s.EndTime
			}
		}
		if latestStart.After(earliestEnd) {
			t.Errorf("tool spans did not overlap — latest start %v is after earliest end %v", latestStart, earliestEnd)
		}
	}
}

// ---------------------------------------------------------------------------
// Inference config attributes on spans
// ---------------------------------------------------------------------------

func TestInvokeSpan_InferenceConfigAttributes(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{
		Text:  "hello",
		Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
	})

	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithTemperature(0.7),
		agent.WithTopP(0.9),
		agent.WithTopK(50),
		agent.WithStopSequences([]string{"STOP", "END"}),
		agent.WithMaxTokens(2048),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()

	// Check agent.invoke span
	invokeSpan := findSpan(spans, "agent.invoke")
	if invokeSpan == nil {
		t.Fatal("expected agent.invoke span")
	}
	if v := getAttr(*invokeSpan, AttrGenAITemperature); v.AsFloat64() != 0.7 {
		t.Errorf("invoke: expected temperature=0.7, got %v", v.AsFloat64())
	}
	if v := getAttr(*invokeSpan, AttrGenAITopP); v.AsFloat64() != 0.9 {
		t.Errorf("invoke: expected top_p=0.9, got %v", v.AsFloat64())
	}
	if v := getAttr(*invokeSpan, AttrGenAITopK); v.AsInt64() != 50 {
		t.Errorf("invoke: expected top_k=50, got %v", v.AsInt64())
	}
	if v := getAttr(*invokeSpan, AttrGenAIMaxTokens); v.AsInt64() != 2048 {
		t.Errorf("invoke: expected max_tokens=2048, got %v", v.AsInt64())
	}
	stopSeqs := getAttr(*invokeSpan, AttrGenAIStopSequences)
	if ss := stopSeqs.AsStringSlice(); len(ss) != 2 || ss[0] != "STOP" || ss[1] != "END" {
		t.Errorf("invoke: expected stop_sequences=[STOP END], got %v", stopSeqs.AsStringSlice())
	}

	// Check agent.provider.call span
	provSpan := findSpan(spans, "agent.provider.call")
	if provSpan == nil {
		t.Fatal("expected agent.provider.call span")
	}
	if v := getAttr(*provSpan, AttrGenAITemperature); v.AsFloat64() != 0.7 {
		t.Errorf("provider: expected temperature=0.7, got %v", v.AsFloat64())
	}
	if v := getAttr(*provSpan, AttrGenAITopP); v.AsFloat64() != 0.9 {
		t.Errorf("provider: expected top_p=0.9, got %v", v.AsFloat64())
	}
	if v := getAttr(*provSpan, AttrGenAITopK); v.AsInt64() != 50 {
		t.Errorf("provider: expected top_k=50, got %v", v.AsInt64())
	}
	if v := getAttr(*provSpan, AttrGenAIMaxTokens); v.AsInt64() != 2048 {
		t.Errorf("provider: expected max_tokens=2048, got %v", v.AsInt64())
	}
}

func TestInvokeSpan_NoInferenceConfigAttributes_WhenNoneSet(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	invokeSpan := findSpan(spans, "agent.invoke")
	if invokeSpan == nil {
		t.Fatal("expected agent.invoke span")
	}

	// None of the inference config attributes should be present.
	for _, key := range []string{AttrGenAITemperature, AttrGenAITopP, AttrGenAITopK, AttrGenAIMaxTokens, AttrGenAIStopSequences} {
		v := getAttr(*invokeSpan, key)
		if v.Type() != 0 { // 0 = INVALID (not set)
			t.Errorf("expected no %s attribute when no inference config set, got %v", key, v)
		}
	}
}

func TestInvokeSpan_PerInvocationOverride_ReflectedInSpan(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{
		Text:  "hello",
		Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
	})

	// Agent-level: temperature=0.3
	a, err := agent.New(prov, prompt.Text("sys"), nil,
		agent.WithTemperature(0.3),
		WithTracing(tp),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Per-invocation override: temperature=0.95
	temp := 0.95
	ctx := agent.WithInferenceConfig(context.Background(), &agent.InferenceConfig{
		Temperature: &temp,
	})

	_, _, err = a.Invoke(ctx, "hi")
	if err != nil {
		t.Fatal(err)
	}

	spans := exp.GetSpans()
	invokeSpan := findSpan(spans, "agent.invoke")
	if invokeSpan == nil {
		t.Fatal("expected agent.invoke span")
	}

	// The span should reflect the merged value (per-invocation wins).
	if v := getAttr(*invokeSpan, AttrGenAITemperature); v.AsFloat64() != 0.95 {
		t.Errorf("expected temperature=0.95 (per-invocation override), got %v", v.AsFloat64())
	}
}
