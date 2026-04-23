package slog

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// captureHandler is a slog.Handler that stores all log records for assertion.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
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

// TestWithLogging_InstallsHook verifies WithLogging sets LoggingHook on agent.
// Validates: Requirement 9.4
func TestWithLogging_InstallsHook(t *testing.T) {
	ch := &captureHandler{}
	opt := WithLogging(WithHandler(ch))

	a, err := agent.New(testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "ok"})), prompt.Text("sys"), nil, opt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.LoggingHook() == nil {
		t.Fatal("expected LoggingHook to be set after WithLogging")
	}
}

// TestWithGraphLogging_InstallsHook verifies WithGraphLogging sets GraphLoggingHook on graph.
// Validates: Requirement 9.5
func TestWithGraphLogging_InstallsHook(t *testing.T) {
	ch := &captureHandler{}
	opt := WithGraphLogging(WithHandler(ch))

	g, err := graph.NewGraph(opt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if g.GraphLoggingHook() == nil {
		t.Fatal("expected GraphLoggingHook to be set after WithGraphLogging")
	}
}

// TestWithSwarmLogging_InstallsHook verifies WithSwarmLogging sets SwarmLoggingHook on swarm.
// Validates: Requirement 9.6
func TestWithSwarmLogging_InstallsHook(t *testing.T) {
	ch := &captureHandler{}
	opt := WithSwarmLogging(WithHandler(ch))

	a1, err := agent.New(testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "ok"})), prompt.Text("agent1"), nil)
	if err != nil {
		t.Fatalf("unexpected error creating agent1: %v", err)
	}
	a2, err := agent.New(testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "ok"})), prompt.Text("agent2"), nil)
	if err != nil {
		t.Fatalf("unexpected error creating agent2: %v", err)
	}

	s, err := agent.NewSwarm([]agent.SwarmMember{
		{Name: "a1", Description: "first", Agent: a1},
		{Name: "a2", Description: "second", Agent: a2},
	}, opt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.SwarmLoggingHook() == nil {
		t.Fatal("expected SwarmLoggingHook to be set after WithSwarmLogging")
	}
}

// TestWithHandler_CustomHandler verifies custom slog.Handler receives log entries.
// Validates: Requirement 4.4
func TestWithHandler_CustomHandler(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.OnInvokeStart(agent.InvokeSpanParams{ModelID: "test-model"})

	records := ch.getRecords()
	if len(records) == 0 {
		t.Fatal("expected custom handler to receive log entries, got 0")
	}
	if records[0].Message != "invoke.start" {
		t.Errorf("expected message %q, got %q", "invoke.start", records[0].Message)
	}
}

// TestDefaultHandler verifies default uses slog.Default().
// Validates: Requirement 4.5
func TestDefaultHandler(t *testing.T) {
	h := newSlogHook(nil)

	// The default logger should be slog.Default(). We verify by checking
	// that the hook's logger is non-nil and that calling a method doesn't panic.
	if h.logger == nil {
		t.Fatal("expected default logger to be set")
	}

	// Verify it's the default logger by comparing handler types.
	// slog.Default() returns the package-level default logger.
	defaultHandler := slog.Default().Handler()
	hookHandler := h.logger.Handler()

	// Both should be the same handler instance when no custom handler is provided.
	if defaultHandler != hookHandler {
		t.Error("expected hook to use slog.Default() handler when no custom handler is provided")
	}
}

// TestWithMinLevel_FiltersBelow verifies entries below min level are not emitted.
// Validates: Requirement 4.8
func TestWithMinLevel_FiltersBelow(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch), WithMinLevel(slog.LevelInfo)})

	// Debug events should be filtered out.
	h.OnInvokeStart(agent.InvokeSpanParams{})                    // Debug
	h.OnIterationStart(1)                                        // Debug
	h.OnToolStart("test-tool")                                   // Debug
	h.OnInvokeEnd(nil, agent.TokenUsage{}, 100*time.Millisecond) // Info — should pass

	records := ch.getRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record (only Info+), got %d", len(records))
	}
	if records[0].Message != "invoke.end" {
		t.Errorf("expected message %q, got %q", "invoke.end", records[0].Message)
	}
}

// TestLogLevel_DebugForStarts verifies start events emit at Debug level.
// Validates: Requirement 4.6
func TestLogLevel_DebugForStarts(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.OnInvokeStart(agent.InvokeSpanParams{})
	h.OnIterationStart(1)
	h.OnProviderCallStart("model-1")
	h.OnToolStart("my-tool")
	h.OnMemoryStart("load", "conv-1")
	h.OnRetrieverStart("query")
	h.OnGraphRunStart()
	h.OnNodeStart("node-a")
	h.OnSwarmRunStart("agent-1", 3, 10)
	h.OnSwarmAgentStart("agent-1")

	records := ch.getRecords()
	if len(records) != 10 {
		t.Fatalf("expected 10 records, got %d", len(records))
	}
	for i, r := range records {
		if r.Level != slog.LevelDebug {
			t.Errorf("record %d (%s): expected Debug level, got %v", i, r.Message, r.Level)
		}
	}
}

// TestLogLevel_InfoForEnds verifies end events emit at Info level.
// Validates: Requirement 4.6
func TestLogLevel_InfoForEnds(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	dur := 50 * time.Millisecond
	usage := agent.TokenUsage{InputTokens: 10, OutputTokens: 5}

	h.OnInvokeEnd(nil, usage, dur)
	h.OnProviderCallEnd(nil, usage, 2, dur)
	h.OnToolEnd("my-tool", nil, dur)
	h.OnMemoryEnd("save", "conv-1", nil, 5, dur)
	h.OnRetrieverEnd(nil, 3, dur)
	h.OnGraphRunEnd(nil, 5, agent.TokenUsage{InputTokens: 100, OutputTokens: 50}, dur)
	h.OnNodeEnd("node-a", nil, dur)
	h.OnSwarmRunEnd(nil, agent.SwarmResult{FinalAgent: "a1"}, dur)
	h.OnSwarmAgentEnd("agent-1", nil, dur)
	h.OnSwarmHandoff("a1", "a2")

	records := ch.getRecords()
	if len(records) != 10 {
		t.Fatalf("expected 10 records, got %d", len(records))
	}
	for i, r := range records {
		if r.Level != slog.LevelInfo {
			t.Errorf("record %d (%s): expected Info level, got %v", i, r.Message, r.Level)
		}
	}
}

// TestLogLevel_ErrorOnFailure verifies end events with error emit at Error level.
// Validates: Requirement 4.6
func TestLogLevel_ErrorOnFailure(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	testErr := errors.New("something failed")
	dur := 50 * time.Millisecond
	usage := agent.TokenUsage{}

	h.OnInvokeEnd(testErr, usage, dur)
	h.OnProviderCallEnd(testErr, usage, 0, dur)
	h.OnToolEnd("my-tool", testErr, dur)
	h.OnMemoryEnd("load", "conv-1", testErr, 0, dur)
	h.OnRetrieverEnd(testErr, 0, dur)
	h.OnGraphRunEnd(testErr, 0, agent.TokenUsage{}, dur)
	h.OnNodeEnd("node-a", testErr, dur)
	h.OnSwarmRunEnd(testErr, agent.SwarmResult{}, dur)
	h.OnSwarmAgentEnd("agent-1", testErr, dur)
	h.OnGuardrailComplete("input", false, testErr)

	records := ch.getRecords()
	if len(records) != 10 {
		t.Fatalf("expected 10 records, got %d", len(records))
	}
	for i, r := range records {
		if r.Level != slog.LevelError {
			t.Errorf("record %d (%s): expected Error level, got %v", i, r.Message, r.Level)
		}
	}
}

// TestLogLevel_WarnForMaxIterations verifies max iterations emits at Warn level.
// Validates: Requirement 4.6
func TestLogLevel_WarnForMaxIterations(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.OnMaxIterationsExceeded(10)

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
// Validates: Requirement 4.6
func TestLogLevel_WarnForGuardrailBlock(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.OnGuardrailComplete("output", true, nil)

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
// Validates: Requirement 4.7
func TestStructuredAttributes(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})
	h.agentName = "test-agent"

	h.OnInvokeStart(agent.InvokeSpanParams{
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
// Validates: Requirement 4.7
func TestInvokeEnd_IncludesTokenUsage(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.OnInvokeEnd(nil, agent.TokenUsage{InputTokens: 150, OutputTokens: 42}, 200*time.Millisecond)

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

// TestToolEnd_IncludesDuration verifies tool end includes duration_ms.
// Validates: Requirement 4.7
func TestToolEnd_IncludesDuration(t *testing.T) {
	ch := &captureHandler{}
	h := newSlogHook([]Option{WithHandler(ch)})

	h.OnToolEnd("my-tool", nil, 123*time.Millisecond)

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
