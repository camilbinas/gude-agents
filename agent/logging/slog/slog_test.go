package slog

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// captureHandler is a slog.Handler that stores all log records for assertion.
type captureHandler struct {
	mu       sync.Mutex
	records  []slog.Record
	contexts []context.Context
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	h.contexts = append(h.contexts, ctx)
	return nil
}

func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *captureHandler) getRecords() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]slog.Record, len(h.records))
	copy(cp, h.records)
	return cp
}

func (h *captureHandler) getContexts() []context.Context {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]context.Context, len(h.contexts))
	copy(cp, h.contexts)
	return cp
}

// recordAttrs extracts all attributes from a slog.Record into a map.
func recordAttrs(r slog.Record) map[string]slog.Value {
	attrs := make(map[string]slog.Value)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value
		return true
	})
	return attrs
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestWithLogging_InstallsObserver verifies WithLogging registers a valid observer.
func TestWithLogging_InstallsObserver(t *testing.T) {
	ch := &captureHandler{}
	opt := WithLogging(WithHandler(ch))

	if _, err := agent.New(testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "ok"})), "sys", opt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWithHandler_CustomHandler verifies custom slog.Handler receives log entries.
func TestWithHandler_CustomHandler(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.ObserveInvoke(context.Background(), agent.InvokeRecord{Phase: agent.Start, ModelID: "test-model"})

	records := ch.getRecords()
	if len(records) == 0 {
		t.Fatal("expected custom handler to receive log entries, got 0")
	}
	if records[0].Message != "invoke.start" {
		t.Errorf("expected message %q, got %q", "invoke.start", records[0].Message)
	}
}

// TestDefaultHandler verifies default uses slog.Default().
func TestDefaultHandler(t *testing.T) {
	h := newSlogHook(nil)

	// The default logger should be slog.Default(). We verify by checking
	// that the observer's logger is non-nil and that calling a method doesn't panic.
	if h.logger == nil {
		t.Fatal("expected default logger to be set")
	}

	// Verify it's the default logger by comparing handler types.
	// slog.Default() returns the package-level default logger.
	defaultHandler := slog.Default().Handler()
	observerHandler := h.logger.Handler()

	// Both should be the same handler instance when no custom handler is provided.
	if defaultHandler != observerHandler {
		t.Error("expected observer to use slog.Default() handler when no custom handler is provided")
	}
}

// TestWithMinLevel_FiltersBelow verifies entries below min level are not emitted.
func TestWithMinLevel_FiltersBelow(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch), WithMinLevel(slog.LevelInfo)})
	ctx := context.Background()

	// Debug events should be filtered out.
	h.ObserveInvoke(ctx, agent.InvokeRecord{Phase: agent.Start})
	h.ObserveIteration(ctx, agent.IterationRecord{Phase: agent.Start, Iteration: 1})
	h.ObserveTool(ctx, agent.ToolCallRecord{Phase: agent.Start, Name: "test-tool"})
	h.ObserveInvoke(ctx, agent.InvokeRecord{Phase: agent.End, Duration: 100 * time.Millisecond})

	records := ch.getRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record (only Info+), got %d", len(records))
	}
	if records[0].Message != "invoke.end" {
		t.Errorf("expected message %q, got %q", "invoke.end", records[0].Message)
	}
}

// TestLogLevel_DebugForStarts verifies start events emit at Debug level.
func TestLogLevel_DebugForStarts(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})
	ctx := context.Background()

	h.ObserveInvoke(ctx, agent.InvokeRecord{Phase: agent.Start})
	h.ObserveIteration(ctx, agent.IterationRecord{Phase: agent.Start, Iteration: 1})
	h.ObserveModel(ctx, agent.ModelCallRecord{Phase: agent.Start, ModelID: "model-1"})
	h.ObserveTool(ctx, agent.ToolCallRecord{Phase: agent.Start, Name: "my-tool"})
	h.ObserveConversation(ctx, agent.ConversationRecord{Phase: agent.Start, Operation: "load", ConversationID: "conv-1"})
	h.ObserveRetrieval(ctx, agent.RetrievalRecord{Phase: agent.Start, Query: "query"})

	records := ch.getRecords()
	if len(records) != 6 {
		t.Fatalf("expected 6 records, got %d", len(records))
	}
	for i, r := range records {
		if r.Level != slog.LevelDebug {
			t.Errorf("record %d (%s): expected Debug level, got %v", i, r.Message, r.Level)
		}
	}
}

// TestLogLevel_InfoForEnds verifies end events emit at Info level.
func TestLogLevel_InfoForEnds(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})
	ctx := context.Background()

	dur := 50 * time.Millisecond
	usage := agent.TokenUsage{InputTokens: 10, OutputTokens: 5}

	h.ObserveInvoke(ctx, agent.InvokeRecord{Phase: agent.End, Usage: usage, Duration: dur})
	h.ObserveModel(ctx, agent.ModelCallRecord{Phase: agent.End, Usage: usage, ToolCallCount: 2, Duration: dur})
	h.ObserveTool(ctx, agent.ToolCallRecord{Phase: agent.End, Name: "my-tool", Duration: dur})
	h.ObserveConversation(ctx, agent.ConversationRecord{Phase: agent.End, Operation: "save", ConversationID: "conv-1", MessageCount: 5, Duration: dur})
	h.ObserveRetrieval(ctx, agent.RetrievalRecord{Phase: agent.End, DocumentCount: 3, Duration: dur})

	records := ch.getRecords()
	if len(records) != 5 {
		t.Fatalf("expected 5 records, got %d", len(records))
	}
	for i, r := range records {
		if r.Level != slog.LevelInfo {
			t.Errorf("record %d (%s): expected Info level, got %v", i, r.Message, r.Level)
		}
	}
}

// TestLogLevel_ErrorOnFailure verifies end events with error emit at Error level.
func TestLogLevel_ErrorOnFailure(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})
	ctx := context.Background()

	testErr := errors.New("something failed")
	dur := 50 * time.Millisecond

	h.ObserveInvoke(ctx, agent.InvokeRecord{Phase: agent.End, Err: testErr, Duration: dur})
	h.ObserveModel(ctx, agent.ModelCallRecord{Phase: agent.End, Err: testErr, Duration: dur})
	h.ObserveTool(ctx, agent.ToolCallRecord{Phase: agent.End, Name: "my-tool", Err: testErr, Duration: dur})
	h.ObserveConversation(ctx, agent.ConversationRecord{Phase: agent.End, Operation: "load", ConversationID: "conv-1", Err: testErr, Duration: dur})
	h.ObserveRetrieval(ctx, agent.RetrievalRecord{Phase: agent.End, Err: testErr, Duration: dur})
	h.ObserveGuardrail(ctx, agent.GuardrailRecord{Phase: agent.End, Direction: "input", Err: testErr})

	records := ch.getRecords()
	if len(records) != 6 {
		t.Fatalf("expected 6 records, got %d", len(records))
	}
	for i, r := range records {
		if r.Level != slog.LevelError {
			t.Errorf("record %d (%s): expected Error level, got %v", i, r.Message, r.Level)
		}
	}
}

// TestLogLevel_WarnForMaxIterations verifies max iterations emits at Warn level.
func TestLogLevel_WarnForMaxIterations(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.ObserveLimit(context.Background(), agent.LimitRecord{Phase: agent.End, Name: "max_iterations", Limit: 10})

	records := ch.getRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Level != slog.LevelWarn {
		t.Errorf("expected Warn level, got %v", records[0].Level)
	}
	if records[0].Message != "max_iterations_exceeded" {
		t.Errorf("expected message %q, got %q", "max_iterations_exceeded", records[0].Message)
	}
}

// TestLogLevel_WarnForGuardrailBlock verifies guardrail block emits at Warn level.
func TestLogLevel_WarnForGuardrailBlock(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.ObserveGuardrail(context.Background(), agent.GuardrailRecord{Phase: agent.End, Direction: "output", Blocked: true})

	records := ch.getRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Level != slog.LevelWarn {
		t.Errorf("expected Warn level, got %v", records[0].Level)
	}
	if records[0].Message != "guardrail.complete" {
		t.Errorf("expected message %q, got %q", "guardrail.complete", records[0].Message)
	}

	attrs := recordAttrs(records[0])
	if v, ok := attrs["blocked"]; !ok || !v.Bool() {
		t.Error("expected blocked=true attribute")
	}
	if v, ok := attrs["direction"]; !ok || v.String() != "output" {
		t.Errorf("expected direction=output, got %v", v)
	}
}

// TestStructuredAttributes verifies log entries contain expected key-value attributes.
func TestStructuredAttributes(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.ObserveInvoke(context.Background(), agent.InvokeRecord{
		Phase:          agent.Start,
		AgentName:      "test-agent",
		ModelID:        "claude-3",
		ConversationID: "conv-123",
		MaxIterations:  10,
	})

	records := ch.getRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	attrs := recordAttrs(records[0])

	checks := map[string]string{
		"agent.name":      "test-agent",
		"model.id":        "claude-3",
		"conversation_id": "conv-123",
	}
	for key, want := range checks {
		v, ok := attrs[key]
		if !ok {
			t.Errorf("missing attribute %q", key)
			continue
		}
		if v.String() != want {
			t.Errorf("attribute %q: expected %q, got %q", key, want, v.String())
		}
	}

	if v, ok := attrs["max_iterations"]; !ok {
		t.Error("missing attribute max_iterations")
	} else if v.Int64() != 10 {
		t.Errorf("max_iterations: expected 10, got %d", v.Int64())
	}
}

// TestInvokeEnd_IncludesTokenUsage verifies invoke end includes input_tokens and output_tokens.
func TestInvokeEnd_IncludesTokenUsage(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.ObserveInvoke(context.Background(), agent.InvokeRecord{
		Phase:    agent.End,
		Usage:    agent.TokenUsage{InputTokens: 150, OutputTokens: 42},
		Duration: 200 * time.Millisecond,
	})

	records := ch.getRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	attrs := recordAttrs(records[0])

	if v, ok := attrs["input_tokens"]; !ok {
		t.Error("missing attribute input_tokens")
	} else if v.Int64() != 150 {
		t.Errorf("input_tokens: expected 150, got %d", v.Int64())
	}

	if v, ok := attrs["output_tokens"]; !ok {
		t.Error("missing attribute output_tokens")
	} else if v.Int64() != 42 {
		t.Errorf("output_tokens: expected 42, got %d", v.Int64())
	}

	if _, ok := attrs["duration_ms"]; !ok {
		t.Error("missing attribute duration_ms")
	}
}

func TestInvokeEnd_LogsResponseText(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.ObserveInvoke(context.Background(), agent.InvokeRecord{Phase: agent.End, Response: "hello"})

	records := ch.getRecords()
	if len(records) != 2 {
		t.Fatalf("expected invoke end and response records, got %d", len(records))
	}
	if records[1].Message != "response.text" {
		t.Fatalf("expected response.text, got %q", records[1].Message)
	}
	if got := recordAttrs(records[1])["text"].String(); got != "hello" {
		t.Errorf("expected response text %q, got %q", "hello", got)
	}
}

func TestObserverUsesSuppliedContext(t *testing.T) {
	type contextKey struct{}
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})
	ctx := context.WithValue(context.Background(), contextKey{}, "value")

	returned := h.ObserveTool(ctx, agent.ToolCallRecord{Phase: agent.Start, Name: "tool"})
	if returned != ctx {
		t.Fatal("expected observer to return the supplied context")
	}
	contexts := ch.getContexts()
	if len(contexts) != 1 || contexts[0].Value(contextKey{}) != "value" {
		t.Fatal("expected slog handler to receive the supplied context")
	}
}

// TestToolEnd_IncludesDuration verifies tool end includes duration_ms.
func TestToolEnd_IncludesDuration(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.ObserveTool(context.Background(), agent.ToolCallRecord{Phase: agent.End, Name: "my-tool", Duration: 123 * time.Millisecond})

	records := ch.getRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	attrs := recordAttrs(records[0])

	if v, ok := attrs["tool.name"]; !ok {
		t.Error("missing attribute tool.name")
	} else if v.String() != "my-tool" {
		t.Errorf("tool.name: expected %q, got %q", "my-tool", v.String())
	}

	if v, ok := attrs["duration_ms"]; !ok {
		t.Error("missing attribute duration_ms")
	} else if v.Float64() != 123.0 {
		t.Errorf("duration_ms: expected 123, got %v", v.Float64())
	}
}
