package slog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

// genError generates a random error: nil roughly half the time, non-nil otherwise.
func genError(t *rapid.T, name string) error {
	if rapid.Bool().Draw(t, name+"_isErr") {
		return errors.New(rapid.StringMatching(`[a-z]{3,20}`).Draw(t, name+"_msg"))
	}
	return nil
}

// genDuration generates a random positive duration.
func genDuration(t *rapid.T, name string) time.Duration {
	ms := rapid.Int64Range(0, 60000).Draw(t, name)
	return time.Duration(ms) * time.Millisecond
}

// genTokenUsage generates a random TokenUsage with non-negative token counts.
func genTokenUsage(t *rapid.T, name string) agent.TokenUsage {
	return agent.TokenUsage{
		InputTokens:  rapid.IntRange(0, 100000).Draw(t, name+"_input"),
		OutputTokens: rapid.IntRange(0, 100000).Draw(t, name+"_output"),
	}
}

// genString generates a random non-empty alphanumeric string.
func genString(t *rapid.T, name string) string {
	return rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,30}`).Draw(t, name)
}

// genSlogLevel generates a random slog.Level from the standard set.
func genSlogLevel(t *rapid.T, name string) slog.Level {
	return rapid.SampledFrom([]slog.Level{
		slog.LevelDebug,
		slog.LevelInfo,
		slog.LevelWarn,
		slog.LevelError,
	}).Draw(t, name)
}

// lifecycleEvent represents a single lifecycle observation.
type lifecycleEvent struct {
	name          string
	category      string
	expectedLevel slog.Level
	fire          func(context.Context, *slogHook)
	err           error
}

// genLifecycleEvent generates a random lifecycle observation with random parameters.
func genLifecycleEvent(t *rapid.T, idx int) lifecycleEvent {
	prefix := fmt.Sprintf("evt_%d", idx)
	eventType := rapid.IntRange(0, 12).Draw(t, prefix+"_type")

	switch eventType {
	case 0: // InvokeStart
		record := agent.InvokeRecord{
			Phase:          agent.Start,
			AgentName:      genString(t, prefix+"_agentName"),
			ModelID:        genString(t, prefix+"_modelID"),
			ConversationID: genString(t, prefix+"_convID"),
			MaxIterations:  rapid.IntRange(1, 100).Draw(t, prefix+"_maxIter"),
		}
		return lifecycleEvent{
			name:          "invoke.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveInvoke(ctx, record) },
		}
	case 1: // InvokeEnd
		err := genError(t, prefix)
		record := agent.InvokeRecord{
			Phase:    agent.End,
			Err:      err,
			Usage:    genTokenUsage(t, prefix),
			Duration: genDuration(t, prefix+"_dur"),
		}
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "invoke.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveInvoke(ctx, record) },
			err:           err,
		}
	case 2: // IterationStart
		record := agent.IterationRecord{Phase: agent.Start, Iteration: rapid.IntRange(1, 100).Draw(t, prefix+"_iter")}
		return lifecycleEvent{
			name:          "iteration.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveIteration(ctx, record) },
		}
	case 3: // ModelStart
		record := agent.ModelCallRecord{Phase: agent.Start, ModelID: genString(t, prefix+"_modelID")}
		return lifecycleEvent{
			name:          "provider_call.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveModel(ctx, record) },
		}
	case 4: // ModelEnd
		err := genError(t, prefix)
		record := agent.ModelCallRecord{
			Phase:         agent.End,
			Err:           err,
			Usage:         genTokenUsage(t, prefix),
			ToolCallCount: rapid.IntRange(0, 20).Draw(t, prefix+"_toolCount"),
			Duration:      genDuration(t, prefix+"_dur"),
		}
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "provider_call.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveModel(ctx, record) },
			err:           err,
		}
	case 5: // ToolStart
		record := agent.ToolCallRecord{Phase: agent.Start, Name: genString(t, prefix+"_tool")}
		return lifecycleEvent{
			name:          "tool.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveTool(ctx, record) },
		}
	case 6: // ToolEnd
		err := genError(t, prefix)
		record := agent.ToolCallRecord{
			Phase:    agent.End,
			Name:     genString(t, prefix+"_tool"),
			Err:      err,
			Duration: genDuration(t, prefix+"_dur"),
		}
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "tool.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveTool(ctx, record) },
			err:           err,
		}
	case 7: // GuardrailComplete
		err := genError(t, prefix)
		record := agent.GuardrailRecord{
			Phase:     agent.End,
			Direction: rapid.SampledFrom([]string{"input", "output"}).Draw(t, prefix+"_dir"),
			Blocked:   rapid.Bool().Draw(t, prefix+"_blocked"),
			Err:       err,
		}
		lvl := slog.LevelDebug
		if record.Blocked {
			lvl = slog.LevelWarn
		}
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "guardrail.complete",
			category:      "guardrail",
			expectedLevel: lvl,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveGuardrail(ctx, record) },
			err:           err,
		}
	case 8: // ConversationStart
		record := agent.ConversationRecord{
			Phase:          agent.Start,
			Operation:      rapid.SampledFrom([]string{"load", "save"}).Draw(t, prefix+"_op"),
			ConversationID: genString(t, prefix+"_convID"),
		}
		return lifecycleEvent{
			name:          "memory.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveConversation(ctx, record) },
		}
	case 9: // ConversationEnd
		err := genError(t, prefix)
		record := agent.ConversationRecord{
			Phase:          agent.End,
			Operation:      rapid.SampledFrom([]string{"load", "save"}).Draw(t, prefix+"_op"),
			ConversationID: genString(t, prefix+"_convID"),
			MessageCount:   5,
			Err:            err,
			Duration:       genDuration(t, prefix+"_dur"),
		}
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "memory.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveConversation(ctx, record) },
			err:           err,
		}
	case 10: // RetrievalStart
		record := agent.RetrievalRecord{Phase: agent.Start, Query: genString(t, prefix+"_query")}
		return lifecycleEvent{
			name:          "retriever.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveRetrieval(ctx, record) },
		}
	case 11: // RetrievalEnd
		err := genError(t, prefix)
		record := agent.RetrievalRecord{
			Phase:         agent.End,
			DocumentCount: rapid.IntRange(0, 100).Draw(t, prefix+"_docCount"),
			Err:           err,
			Duration:      genDuration(t, prefix+"_dur"),
		}
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "retriever.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveRetrieval(ctx, record) },
			err:           err,
		}
	default: // MaxIterationsExceeded
		record := agent.LimitRecord{
			Phase: agent.End,
			Name:  "max_iterations",
			Limit: rapid.IntRange(1, 100).Draw(t, prefix+"_limit"),
		}
		return lifecycleEvent{
			name:          "max_iterations_exceeded",
			category:      "warn",
			expectedLevel: slog.LevelWarn,
			fire:          func(ctx context.Context, h *slogHook) { h.ObserveLimit(ctx, record) },
		}
	}
}

// ---------------------------------------------------------------------------
// Property 1: Log level mapping correctness
// ---------------------------------------------------------------------------

// TestProperty_1_LogLevelMappingCorrectness verifies normalized lifecycle
// records map to the expected log levels and message names.
func TestProperty_1_LogLevelMappingCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 30).Draw(rt, "numEvents")
		ch := &captureHandler{}
		h := newSlogHook([]Option{WithHandler(ch)})
		events := make([]lifecycleEvent, n)
		for i := range n {
			events[i] = genLifecycleEvent(rt, i)
		}
		ctx := context.Background()
		for _, evt := range events {
			evt.fire(ctx, h)
		}

		records := ch.getRecords()
		if len(records) != n {
			rt.Fatalf("expected %d records, got %d", n, len(records))
		}
		for i, evt := range events {
			r := records[i]
			if r.Level != evt.expectedLevel {
				rt.Fatalf("event %d (%s, category=%s): expected level %v, got %v", i, evt.name, evt.category, evt.expectedLevel, r.Level)
			}
			if r.Message != evt.name {
				rt.Fatalf("event %d: expected message %q, got %q", i, evt.name, r.Message)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 2: MinLevel filtering completeness
// ---------------------------------------------------------------------------

// TestProperty_2_MinLevelFilteringCompleteness verifies every record at or
// above a random minimum level is emitted in order and lower records are not.
func TestProperty_2_MinLevelFilteringCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		minLevel := genSlogLevel(rt, "minLevel")
		n := rapid.IntRange(1, 30).Draw(rt, "numEvents")
		ch := &captureHandler{}
		h := newSlogHook([]Option{WithHandler(ch), WithMinLevel(minLevel)})
		events := make([]lifecycleEvent, n)
		for i := range n {
			events[i] = genLifecycleEvent(rt, i)
		}
		ctx := context.Background()
		for _, evt := range events {
			evt.fire(ctx, h)
		}

		records := ch.getRecords()
		var expectedCount int
		for _, evt := range events {
			if evt.expectedLevel >= minLevel {
				expectedCount++
			}
		}
		if len(records) != expectedCount {
			rt.Fatalf("with minLevel=%v: expected %d records, got %d (total events=%d)", minLevel, expectedCount, len(records), n)
		}
		for i, r := range records {
			if r.Level < minLevel {
				rt.Fatalf("record %d (%s): level %v is below minLevel %v", i, r.Message, r.Level, minLevel)
			}
		}
		recordIdx := 0
		for _, evt := range events {
			if evt.expectedLevel < minLevel {
				continue
			}
			if recordIdx >= len(records) {
				rt.Fatalf("ran out of records: expected event %q at record index %d", evt.name, recordIdx)
			}
			r := records[recordIdx]
			if r.Message != evt.name {
				rt.Fatalf("record %d: expected message %q, got %q", recordIdx, evt.name, r.Message)
			}
			if r.Level != evt.expectedLevel {
				rt.Fatalf("record %d (%s): expected level %v, got %v", recordIdx, r.Message, evt.expectedLevel, r.Level)
			}
			recordIdx++
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: Structured attribute presence
// ---------------------------------------------------------------------------

// TestProperty_3_StructuredAttributePresence verifies normalized records retain
// the structured attribute contract for all existing lifecycle messages.
func TestProperty_3_StructuredAttributePresence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ch := &captureHandler{}
		h := newSlogHook([]Option{WithHandler(ch)})
		ctx := context.Background()
		eventType := rapid.IntRange(0, 11).Draw(rt, "eventType")

		switch eventType {
		case 0: // InvokeStart
			agentName := genString(rt, "agentName")
			modelID := genString(rt, "modelID")
			convID := genString(rt, "convID")
			h.ObserveInvoke(ctx, agent.InvokeRecord{
				Phase: agent.Start, AgentName: agentName, ModelID: modelID,
				ConversationID: convID, MaxIterations: rapid.IntRange(1, 100).Draw(rt, "maxIter"),
			})
			attrs := onlyRecordAttrs(rt, ch)
			requireAttr(rt, attrs, "agent.name", agentName)
			requireAttr(rt, attrs, "model.id", modelID)
			requireAttr(rt, attrs, "conversation_id", convID)
			requireAttrExists(rt, attrs, "max_iterations")

		case 1: // InvokeEnd
			err := genError(rt, "err")
			h.ObserveInvoke(ctx, agent.InvokeRecord{Phase: agent.End, Err: err, Usage: genTokenUsage(rt, "usage"), Duration: genDuration(rt, "dur")})
			attrs := onlyRecordAttrs(rt, ch)
			requireAttrExists(rt, attrs, "duration_ms")
			requireAttrExists(rt, attrs, "input_tokens")
			requireAttrExists(rt, attrs, "output_tokens")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 2: // ModelStart
			modelID := genString(rt, "modelID")
			h.ObserveModel(ctx, agent.ModelCallRecord{Phase: agent.Start, ModelID: modelID})
			requireAttr(rt, onlyRecordAttrs(rt, ch), "model.id", modelID)

		case 3: // ModelEnd
			err := genError(rt, "err")
			h.ObserveModel(ctx, agent.ModelCallRecord{
				Phase: agent.End, Err: err, Usage: genTokenUsage(rt, "usage"),
				ToolCallCount: rapid.IntRange(0, 20).Draw(rt, "toolCount"), Duration: genDuration(rt, "dur"),
			})
			attrs := onlyRecordAttrs(rt, ch)
			for _, key := range []string{"duration_ms", "input_tokens", "output_tokens", "tool_call_count"} {
				requireAttrExists(rt, attrs, key)
			}
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 4: // ToolStart
			toolName := genString(rt, "toolName")
			h.ObserveTool(ctx, agent.ToolCallRecord{Phase: agent.Start, Name: toolName})
			requireAttr(rt, onlyRecordAttrs(rt, ch), "tool.name", toolName)

		case 5: // ToolEnd
			toolName := genString(rt, "toolName")
			err := genError(rt, "err")
			h.ObserveTool(ctx, agent.ToolCallRecord{Phase: agent.End, Name: toolName, Err: err, Duration: genDuration(rt, "dur")})
			attrs := onlyRecordAttrs(rt, ch)
			requireAttr(rt, attrs, "tool.name", toolName)
			requireAttrExists(rt, attrs, "duration_ms")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 6: // GuardrailComplete
			direction := rapid.SampledFrom([]string{"input", "output"}).Draw(rt, "dir")
			err := genError(rt, "err")
			h.ObserveGuardrail(ctx, agent.GuardrailRecord{Phase: agent.End, Direction: direction, Blocked: rapid.Bool().Draw(rt, "blocked"), Err: err})
			attrs := onlyRecordAttrs(rt, ch)
			requireAttr(rt, attrs, "direction", direction)
			requireAttrExists(rt, attrs, "blocked")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 7: // ConversationStart
			op := rapid.SampledFrom([]string{"load", "save"}).Draw(rt, "op")
			convID := genString(rt, "convID")
			h.ObserveConversation(ctx, agent.ConversationRecord{Phase: agent.Start, Operation: op, ConversationID: convID})
			attrs := onlyRecordAttrs(rt, ch)
			requireAttr(rt, attrs, "operation", op)
			requireAttr(rt, attrs, "conversation_id", convID)

		case 8: // ConversationEnd
			op := rapid.SampledFrom([]string{"load", "save"}).Draw(rt, "op")
			convID := genString(rt, "convID")
			err := genError(rt, "err")
			h.ObserveConversation(ctx, agent.ConversationRecord{
				Phase: agent.End, Operation: op, ConversationID: convID, Err: err,
				MessageCount: rapid.IntRange(0, 100).Draw(rt, "msgCount"), Duration: genDuration(rt, "dur"),
			})
			attrs := onlyRecordAttrs(rt, ch)
			requireAttr(rt, attrs, "operation", op)
			requireAttr(rt, attrs, "conversation_id", convID)
			requireAttrExists(rt, attrs, "message_count")
			requireAttrExists(rt, attrs, "duration_ms")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 9: // MaxIterationsExceeded
			h.ObserveLimit(ctx, agent.LimitRecord{Phase: agent.End, Name: "max_iterations", Limit: rapid.IntRange(1, 100).Draw(rt, "limit")})
			requireAttrExists(rt, onlyRecordAttrs(rt, ch), "limit")

		case 10: // RetrievalEnd
			err := genError(rt, "err")
			h.ObserveRetrieval(ctx, agent.RetrievalRecord{
				Phase: agent.End, Err: err, DocumentCount: rapid.IntRange(0, 100).Draw(rt, "docCount"), Duration: genDuration(rt, "dur"),
			})
			attrs := onlyRecordAttrs(rt, ch)
			requireAttrExists(rt, attrs, "doc_count")
			requireAttrExists(rt, attrs, "duration_ms")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		default: // IterationStart
			h.ObserveIteration(ctx, agent.IterationRecord{Phase: agent.Start, Iteration: rapid.IntRange(1, 100).Draw(rt, "iter")})
			requireAttrExists(rt, onlyRecordAttrs(rt, ch), "iteration")
		}
	})
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

func onlyRecordAttrs(t *rapid.T, ch *captureHandler) map[string]slog.Value {
	t.Helper()
	records := ch.getRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	return recordAttrs(records[0])
}

// requireAttr asserts that the attribute map contains the expected string value.
func requireAttr(t *rapid.T, attrs map[string]slog.Value, key string, want string) {
	t.Helper()
	v, ok := attrs[key]
	if !ok {
		t.Fatalf("missing required attribute %q", key)
	}
	if v.String() != want {
		t.Fatalf("attribute %q: expected %q, got %q", key, want, v.String())
	}
}

// requireAttrExists asserts that the attribute map contains the given key.
func requireAttrExists(t *rapid.T, attrs map[string]slog.Value, key string) {
	t.Helper()
	if _, ok := attrs[key]; !ok {
		t.Fatalf("missing required attribute %q", key)
	}
}
