package tracing

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"pgregory.net/rapid"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ===========================================================================
// Task 10: Property-Based Tests
// ===========================================================================

// ---------------------------------------------------------------------------
// 10.1 — Property 1: Invoke span carries configured attributes
// ---------------------------------------------------------------------------

// Feature: otel-tracing, Property 1: Invoke span carries configured attributes
//
// For any agent configuration (max_iterations, model_id, conversation_id),
// when tracing is enabled and the agent is invoked, the agent.invoke span
// contains attributes matching the configured values.
//
// **Validates: Requirements 2.1, 2.2, 12.2**
func TestProperty_InvokeSpanAttributes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxIter := rapid.IntRange(1, 20).Draw(t, "max_iterations")
		modelID := rapid.StringMatching("[a-z][a-z0-9-]{0,20}").Draw(t, "model_id")
		convID := rapid.StringMatching("[a-z][a-z0-9-]{0,20}").Draw(t, "conversation_id")

		exp, tp := newTestTracerProvider()
		defer tp.Shutdown(context.Background())

		mem := newMockMemory()
		prov := newMockProviderWithModel(modelID,
			&agent.ProviderResponse{Text: "ok", Usage: agent.TokenUsage{InputTokens: 1, OutputTokens: 1}},
		)

		a, err := agent.New(prov, prompt.Text("sys"), nil,
			agent.WithMaxIterations(maxIter),
			agent.WithConversation(mem, convID),
			WithTracing(tp),
		)
		if err != nil {
			t.Fatalf("agent.New: %v", err)
		}

		_, _, err = a.Invoke(context.Background(), "hi")
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}

		spans := exp.GetSpans()
		invokeSpan := findSpan(spans, "agent.invoke")
		if invokeSpan == nil {
			t.Fatal("expected agent.invoke span")
		}

		if v := getAttr(*invokeSpan, AttrAgentMaxIterations); v.AsInt64() != int64(maxIter) {
			t.Fatalf("agent.max_iterations: expected %d, got %d", maxIter, v.AsInt64())
		}
		if v := getAttr(*invokeSpan, AttrAgentModelID); v.AsString() != modelID {
			t.Fatalf("agent.model_id: expected %q, got %q", modelID, v.AsString())
		}
		if v := getAttr(*invokeSpan, AttrAgentConversationID); v.AsString() != convID {
			t.Fatalf("agent.conversation_id: expected %q, got %q", convID, v.AsString())
		}
		if v := getAttr(*invokeSpan, AttrGenAISystem); v.AsString() != "gude-agents" {
			t.Fatalf("gen_ai.system: expected %q, got %q", "gude-agents", v.AsString())
		}
	})
}

// ---------------------------------------------------------------------------
// 10.2 — Property 2: Successful invocation records OK status and token usage
// ---------------------------------------------------------------------------

// Feature: otel-tracing, Property 2: Successful invocation records OK status and token usage
//
// For any successful agent invocation with tracing enabled, the agent.invoke
// span has status OK and contains token usage attributes matching the
// cumulative usage.
//
// **Validates: Requirements 2.3**
func TestProperty_SuccessTokenUsage(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		inputTokens := rapid.IntRange(0, 10000).Draw(t, "input_tokens")
		outputTokens := rapid.IntRange(0, 10000).Draw(t, "output_tokens")

		exp, tp := newTestTracerProvider()
		defer tp.Shutdown(context.Background())

		prov := newMockProvider(&agent.ProviderResponse{
			Text:  "ok",
			Usage: agent.TokenUsage{InputTokens: inputTokens, OutputTokens: outputTokens},
		})

		a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
		if err != nil {
			t.Fatalf("agent.New: %v", err)
		}

		_, _, err = a.Invoke(context.Background(), "hi")
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}

		spans := exp.GetSpans()
		invokeSpan := findSpan(spans, "agent.invoke")
		if invokeSpan == nil {
			t.Fatal("expected agent.invoke span")
		}

		if invokeSpan.Status.Code != codes.Ok {
			t.Fatalf("expected OK status, got %v", invokeSpan.Status.Code)
		}
		if v := getAttr(*invokeSpan, AttrAgentTokenUsageInput); v.AsInt64() != int64(inputTokens) {
			t.Fatalf("token_usage.input: expected %d, got %d", inputTokens, v.AsInt64())
		}
		if v := getAttr(*invokeSpan, AttrAgentTokenUsageOutput); v.AsInt64() != int64(outputTokens) {
			t.Fatalf("token_usage.output: expected %d, got %d", outputTokens, v.AsInt64())
		}
	})
}

// ---------------------------------------------------------------------------
// 10.3 — Property 3: Failed operations record Error status
// ---------------------------------------------------------------------------

// Feature: otel-tracing, Property 3: Failed operations record Error status
//
// For any traced operation that fails with a random error, the corresponding
// span has status Error and the error is recorded on the span.
//
// **Validates: Requirements 2.4, 4.3, 5.3, 5.4, 7.4, 9.3, 10.4, 11.3**
func TestProperty_ErrorSpans(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		errMsg := rapid.StringMatching("[a-z][a-z0-9 ]{0,30}").Draw(t, "error_message")

		exp, tp := newTestTracerProvider()
		defer tp.Shutdown(context.Background())

		prov := &errorProvider{err: fmt.Errorf("%s", errMsg)}
		a, err := agent.New(prov, prompt.Text("sys"), nil, WithTracing(tp))
		if err != nil {
			t.Fatalf("agent.New: %v", err)
		}

		_, _, invokeErr := a.Invoke(context.Background(), "hi")
		if invokeErr == nil {
			t.Fatal("expected error from invoke")
		}

		spans := exp.GetSpans()

		// The agent.invoke span should have Error status.
		invokeSpan := findSpan(spans, "agent.invoke")
		if invokeSpan == nil {
			t.Fatal("expected agent.invoke span")
		}
		if invokeSpan.Status.Code != codes.Error {
			t.Fatalf("invoke span: expected Error status, got %v", invokeSpan.Status.Code)
		}

		// The agent.provider.call span should also have Error status.
		provSpan := findSpan(spans, "agent.provider.call")
		if provSpan == nil {
			t.Fatal("expected agent.provider.call span")
		}
		if provSpan.Status.Code != codes.Error {
			t.Fatalf("provider span: expected Error status, got %v", provSpan.Status.Code)
		}

		// Verify the error is recorded on the provider span.
		if !hasErrorEvent(*provSpan) {
			t.Fatal("expected error event on provider span")
		}
	})
}

// hasErrorEvent checks if a span has an "exception" event (recorded by RecordError).
func hasErrorEvent(span tracetest.SpanStub) bool {
	for _, e := range span.Events {
		if e.Name == "exception" {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 10.4 — Property 4: Iteration spans are numbered sequentially
// ---------------------------------------------------------------------------

// Feature: otel-tracing, Property 4: Iteration spans are numbered sequentially
//
// For any agent invocation that runs N iterations, exactly N agent.iteration
// child spans are created with correct 1-based numbering.
//
// **Validates: Requirements 3.1, 3.2**
func TestProperty_IterationNumbering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// N iterations: N-1 tool-call iterations + 1 final text iteration.
		n := rapid.IntRange(1, 10).Draw(t, "iteration_count")

		exp, tp := newTestTracerProvider()
		defer tp.Shutdown(context.Background())

		// Build N-1 tool-call responses + 1 final text response.
		responses := make([]*agent.ProviderResponse, n)
		for i := 0; i < n-1; i++ {
			responses[i] = &agent.ProviderResponse{
				ToolCalls: []tool.Call{tc("tc"+fmt.Sprint(i), "echo")},
				Usage:     agent.TokenUsage{InputTokens: 1, OutputTokens: 1},
			}
		}
		responses[n-1] = &agent.ProviderResponse{
			Text:  "done",
			Usage: agent.TokenUsage{InputTokens: 1, OutputTokens: 1},
		}

		prov := newMockProvider(responses...)
		echoTool := dummyTool("echo", "echoes")

		a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{echoTool},
			agent.WithMaxIterations(n+5), // ensure we don't hit max iterations
			WithTracing(tp),
		)
		if err != nil {
			t.Fatalf("agent.New: %v", err)
		}

		_, _, err = a.Invoke(context.Background(), "hi")
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}

		spans := exp.GetSpans()
		iterSpans := findSpans(spans, "agent.iteration")
		if len(iterSpans) != n {
			t.Fatalf("expected %d iteration spans, got %d", n, len(iterSpans))
		}

		// Verify 1-based sequential numbering.
		for i, s := range iterSpans {
			v := getAttr(s, AttrAgentIterationNumber)
			if v.AsInt64() != int64(i+1) {
				t.Fatalf("iteration[%d]: expected number=%d, got %d", i, i+1, v.AsInt64())
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 10.5 — Property 8: Tool spans are named after the tool
// ---------------------------------------------------------------------------

// Feature: otel-tracing, Property 8: Tool spans are named after the tool
//
// For any tool execution, a span named agent.tool.<tool_name> is created
// and carries a tool.name attribute matching the tool's registered name.
//
// **Validates: Requirements 5.1, 5.2**
func TestProperty_ToolSpanNaming(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		toolName := rapid.StringMatching("[a-z][a-z0-9_]{0,15}").Draw(t, "tool_name")

		exp, tp := newTestTracerProvider()
		defer tp.Shutdown(context.Background())

		prov := newMockProvider(
			&agent.ProviderResponse{
				ToolCalls: []tool.Call{{ToolUseID: "tc1", Name: toolName, Input: json.RawMessage(`{}`)}},
				Usage:     agent.TokenUsage{InputTokens: 1, OutputTokens: 1},
			},
			&agent.ProviderResponse{
				Text:  "done",
				Usage: agent.TokenUsage{InputTokens: 1, OutputTokens: 1},
			},
		)

		theTool := tool.NewRaw(toolName, "a tool", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				return "ok", nil
			})

		a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{theTool}, WithTracing(tp))
		if err != nil {
			t.Fatalf("agent.New: %v", err)
		}

		_, _, err = a.Invoke(context.Background(), "hi")
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}

		spans := exp.GetSpans()
		expectedSpanName := "agent.tool." + toolName
		toolSpan := findSpan(spans, expectedSpanName)
		if toolSpan == nil {
			t.Fatalf("expected span named %q", expectedSpanName)
		}

		if v := getAttr(*toolSpan, AttrToolName); v.AsString() != toolName {
			t.Fatalf("tool.name: expected %q, got %q", toolName, v.AsString())
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Tracing span name consistency (memory-package-rename)
// ---------------------------------------------------------------------------

// Feature: memory-package-rename, Property 5: Tracing span name consistency
//
// For any memory operation string ("load" or "save"), the tracing hook SHALL
// produce a span name of the form "agent.conversation.{operation}".
//
// **Validates: Requirements 15.1, 15.2**
func TestProperty_TracingSpanNameConsistency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		operation := rapid.SampledFrom([]string{"load", "save"}).Draw(t, "operation")
		conversationID := rapid.StringMatching("[a-z][a-z0-9-]{0,20}").Draw(t, "conversation_id")

		exp, tp := newTestTracerProvider()
		defer tp.Shutdown(context.Background())

		hook := &otelHook{tracer: tp.Tracer(instrumentationName)}

		ctx := context.Background()
		ctx, end := hook.OnConversationStart(ctx, operation, conversationID)
		_ = ctx
		end(nil)

		spans := exp.GetSpans()
		expectedSpanName := "agent.conversation." + operation
		span := findSpan(spans, expectedSpanName)
		if span == nil {
			// Collect actual span names for debugging.
			var names []string
			for _, s := range spans {
				names = append(names, s.Name)
			}
			t.Fatalf("expected span named %q, got spans: %v", expectedSpanName, names)
		}

		// Verify the span name does NOT use the old "agent.memory." prefix.
		oldSpanName := "agent.memory." + operation
		if oldSpan := findSpan(spans, oldSpanName); oldSpan != nil {
			t.Fatalf("found old span name %q; expected new name %q", oldSpanName, expectedSpanName)
		}

		// Verify the conversation ID attribute is set.
		if v := getAttr(*span, AttrMemoryConversationID); v.AsString() != conversationID {
			t.Fatalf("memory.conversation_id: expected %q, got %q", conversationID, v.AsString())
		}
	})
}

// ---------------------------------------------------------------------------
// 10.6 — Property 14: Attribute keys follow naming convention
// ---------------------------------------------------------------------------

// Feature: otel-tracing, Property 14: Attribute keys follow naming convention
//
// For all exported attribute key constants, each matches the regex
// ^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$
//
// **Validates: Requirements 12.1**
func TestProperty_AttributeNamingConvention(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

	// All exported attribute key constants.
	allAttrs := []string{
		AttrGenAISystem,
		AttrAgentMaxIterations,
		AttrAgentModelID,
		AttrAgentConversationID,
		AttrAgentTokenUsageInput,
		AttrAgentTokenUsageOutput,
		AttrAgentIterationNumber,
		AttrAgentIterationToolCount,
		AttrAgentIterationFinal,
		AttrProviderModelID,
		AttrProviderInputTokens,
		AttrProviderOutputTokens,
		AttrProviderToolCalls,
		AttrToolName,
		AttrMemoryConversationID,
		AttrRetrieverDocumentCount,
		AttrGraphIterations,
		AttrGenAITemperature,
		AttrGenAITopP,
		AttrGenAITopK,
		AttrGenAIMaxTokens,
		AttrGenAIStopSequences,
	}

	// Also include event name constants.
	allEvents := []string{
		EventMaxIterationsExceeded,
	}

	rapid.Check(t, func(t *rapid.T) {
		// Pick a random attribute from the full set.
		attrKey := rapid.SampledFrom(allAttrs).Draw(t, "attr_key")
		if !pattern.MatchString(attrKey) {
			t.Fatalf("attribute key %q does not match naming convention %s", attrKey, pattern.String())
		}
	})

	// Also verify event names follow the same convention.
	rapid.Check(t, func(t *rapid.T) {
		eventName := rapid.SampledFrom(allEvents).Draw(t, "event_name")
		if !pattern.MatchString(eventName) {
			t.Fatalf("event name %q does not match naming convention %s", eventName, pattern.String())
		}
	})
}

// ---------------------------------------------------------------------------
// 10.7 — Property 15: Structured logger includes trace correlation
// ---------------------------------------------------------------------------

// Feature: otel-tracing, Property 15: Structured logger includes trace correlation
//
// For any log entry emitted by the structured Logger while a span is active,
// the output contains trace_id and span_id fields matching the active span.
//
// **Validates: Requirements 14.2, 14.3**
func TestProperty_StructuredLoggerTraceCorrelation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		spanName := rapid.StringMatching("[a-z][a-z0-9-]{0,20}").Draw(t, "span_name")
		logMsg := rapid.StringMatching("[a-zA-Z0-9 ]{1,30}").Draw(t, "log_message")

		_, tp := newTestTracerProvider()
		defer tp.Shutdown(context.Background())

		tracer := tp.Tracer("test")
		ctx, span := tracer.Start(context.Background(), spanName)
		defer span.End()

		var captured map[string]any
		logger := NewLogger(ctx)
		logger.Output = func(_ context.Context, _ string, fields map[string]any) {
			captured = fields
		}

		logger.Printf("%s", logMsg)

		if captured == nil {
			t.Fatal("expected output to be called")
		}

		sc := span.SpanContext()
		if captured["trace_id"] != sc.TraceID().String() {
			t.Fatalf("trace_id mismatch: expected %s, got %s", sc.TraceID(), captured["trace_id"])
		}
		if captured["span_id"] != sc.SpanID().String() {
			t.Fatalf("span_id mismatch: expected %s, got %s", sc.SpanID(), captured["span_id"])
		}
		if captured["msg"] != logMsg {
			t.Fatalf("msg mismatch: expected %q, got %q", logMsg, captured["msg"])
		}
	})
}
