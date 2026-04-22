package slog

import (
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

// lifecycleEvent represents a single lifecycle event that can be fired on the hook.
type lifecycleEvent struct {
	name          string     // human-readable event name
	category      string     // "start", "end", "warn", "guardrail", "handoff"
	expectedLevel slog.Level // expected log level (before error escalation)
	fire          func(h *slogHook)
	err           error // the error passed (nil for start events)
}

// genLifecycleEvent generates a random lifecycle event with random parameters.
func genLifecycleEvent(t *rapid.T, idx int) lifecycleEvent {
	prefix := fmt.Sprintf("evt_%d", idx)
	eventType := rapid.IntRange(0, 16).Draw(t, prefix+"_type")

	switch eventType {
	case 0: // InvokeStart
		params := agent.InvokeSpanParams{
			ModelID:        genString(t, prefix+"_modelID"),
			ConversationID: genString(t, prefix+"_convID"),
			MaxIterations:  rapid.IntRange(1, 100).Draw(t, prefix+"_maxIter"),
		}
		return lifecycleEvent{
			name:          "invoke.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(h *slogHook) { h.OnInvokeStart(params) },
		}
	case 1: // InvokeEnd
		err := genError(t, prefix)
		usage := genTokenUsage(t, prefix)
		dur := genDuration(t, prefix+"_dur")
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "invoke.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(h *slogHook) { h.OnInvokeEnd(err, usage, dur) },
			err:           err,
		}
	case 2: // IterationStart
		iter := rapid.IntRange(1, 100).Draw(t, prefix+"_iter")
		return lifecycleEvent{
			name:          "iteration.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(h *slogHook) { h.OnIterationStart(iter) },
		}
	case 3: // ProviderCallStart
		modelID := genString(t, prefix+"_modelID")
		return lifecycleEvent{
			name:          "provider_call.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(h *slogHook) { h.OnProviderCallStart(modelID) },
		}
	case 4: // ProviderCallEnd
		err := genError(t, prefix)
		usage := genTokenUsage(t, prefix)
		toolCount := rapid.IntRange(0, 20).Draw(t, prefix+"_toolCount")
		dur := genDuration(t, prefix+"_dur")
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "provider_call.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(h *slogHook) { h.OnProviderCallEnd(err, usage, toolCount, dur) },
			err:           err,
		}
	case 5: // ToolStart
		toolName := genString(t, prefix+"_tool")
		return lifecycleEvent{
			name:          "tool.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(h *slogHook) { h.OnToolStart(toolName) },
		}
	case 6: // ToolEnd
		toolName := genString(t, prefix+"_tool")
		err := genError(t, prefix)
		dur := genDuration(t, prefix+"_dur")
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "tool.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(h *slogHook) { h.OnToolEnd(toolName, err, dur) },
			err:           err,
		}
	case 7: // GuardrailComplete (blocked=false, no error) → Debug
		direction := rapid.SampledFrom([]string{"input", "output"}).Draw(t, prefix+"_dir")
		err := genError(t, prefix)
		blocked := rapid.Bool().Draw(t, prefix+"_blocked")
		lvl := slog.LevelDebug
		if blocked {
			lvl = slog.LevelWarn
		}
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "guardrail.complete",
			category:      "guardrail",
			expectedLevel: lvl,
			fire:          func(h *slogHook) { h.OnGuardrailComplete(direction, blocked, err) },
			err:           err,
		}
	case 8: // MemoryStart
		op := rapid.SampledFrom([]string{"load", "save"}).Draw(t, prefix+"_op")
		convID := genString(t, prefix+"_convID")
		return lifecycleEvent{
			name:          "memory.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(h *slogHook) { h.OnMemoryStart(op, convID) },
		}
	case 9: // MemoryEnd
		op := rapid.SampledFrom([]string{"load", "save"}).Draw(t, prefix+"_op")
		convID := genString(t, prefix+"_convID")
		err := genError(t, prefix)
		dur := genDuration(t, prefix+"_dur")
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "memory.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(h *slogHook) { h.OnMemoryEnd(op, convID, err, 5, dur) },
			err:           err,
		}
	case 10: // RetrieverStart
		query := genString(t, prefix+"_query")
		return lifecycleEvent{
			name:          "retriever.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(h *slogHook) { h.OnRetrieverStart(query) },
		}
	case 11: // RetrieverEnd
		err := genError(t, prefix)
		docCount := rapid.IntRange(0, 100).Draw(t, prefix+"_docCount")
		dur := genDuration(t, prefix+"_dur")
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "retriever.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(h *slogHook) { h.OnRetrieverEnd(err, docCount, dur) },
			err:           err,
		}
	case 12: // MaxIterationsExceeded
		limit := rapid.IntRange(1, 100).Draw(t, prefix+"_limit")
		return lifecycleEvent{
			name:          "max_iterations_exceeded",
			category:      "warn",
			expectedLevel: slog.LevelWarn,
			fire:          func(h *slogHook) { h.OnMaxIterationsExceeded(limit) },
		}
	case 13: // GraphRunStart
		return lifecycleEvent{
			name:          "graph.run.start",
			category:      "start",
			expectedLevel: slog.LevelDebug,
			fire:          func(h *slogHook) { h.OnGraphRunStart() },
		}
	case 14: // GraphRunEnd
		err := genError(t, prefix)
		iterations := rapid.IntRange(0, 100).Draw(t, prefix+"_iterations")
		dur := genDuration(t, prefix+"_dur")
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "graph.run.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(h *slogHook) { h.OnGraphRunEnd(err, iterations, agent.TokenUsage{}, dur) },
			err:           err,
		}
	case 15: // SwarmHandoff
		from := genString(t, prefix+"_from")
		to := genString(t, prefix+"_to")
		return lifecycleEvent{
			name:          "swarm.handoff",
			category:      "handoff",
			expectedLevel: slog.LevelInfo,
			fire:          func(h *slogHook) { h.OnSwarmHandoff(from, to) },
		}
	default: // NodeEnd
		nodeName := genString(t, prefix+"_node")
		err := genError(t, prefix)
		dur := genDuration(t, prefix+"_dur")
		lvl := slog.LevelInfo
		if err != nil {
			lvl = slog.LevelError
		}
		return lifecycleEvent{
			name:          "graph.node.end",
			category:      "end",
			expectedLevel: lvl,
			fire:          func(h *slogHook) { h.OnNodeEnd(nodeName, err, dur) },
			err:           err,
		}
	}
}

// ---------------------------------------------------------------------------
// Property 1: Log level mapping correctness
// ---------------------------------------------------------------------------

// Feature: logging-hook, Property 1: Log level mapping correctness
//
// TestProperty_1_LogLevelMappingCorrectness verifies that for any random
// lifecycle event with random error/nil outcomes, the slog implementation
// maps to the correct log level:
//   - Start events always emit at Debug
//   - End events emit at Info when err==nil, Error when err!=nil
//   - MaxIterationsExceeded always emits at Warn
//   - GuardrailComplete(blocked=true) emits at Warn when err==nil
//   - Any event with err!=nil escalates to Error
//   - SwarmHandoff always emits at Info
//
// **Validates: Requirements 4.6**
func TestProperty_1_LogLevelMappingCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 30).Draw(rt, "numEvents")

		ch := &captureHandler{}
		h := newSlogHook([]Option{WithHandler(ch)})
		h.agentName = "test-agent"

		events := make([]lifecycleEvent, n)
		for i := range n {
			events[i] = genLifecycleEvent(rt, i)
		}

		// Fire all events.
		for _, evt := range events {
			evt.fire(h)
		}

		records := ch.getRecords()
		if len(records) != n {
			rt.Fatalf("expected %d records, got %d", n, len(records))
		}

		// Verify each record's level matches the expected level.
		for i, evt := range events {
			r := records[i]
			if r.Level != evt.expectedLevel {
				rt.Fatalf("event %d (%s, category=%s): expected level %v, got %v",
					i, evt.name, evt.category, evt.expectedLevel, r.Level)
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

// Feature: logging-hook, Property 2: MinLevel filtering completeness
//
// TestProperty_2_MinLevelFilteringCompleteness verifies that for any random
// minimum level and any random sequence of lifecycle events:
//   - No log entry is emitted below the configured min level
//   - All entries at or above the min level are emitted
//
// **Validates: Requirements 4.8**
func TestProperty_2_MinLevelFilteringCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		minLevel := genSlogLevel(rt, "minLevel")
		n := rapid.IntRange(1, 30).Draw(rt, "numEvents")

		ch := &captureHandler{}
		h := newSlogHook([]Option{WithHandler(ch), WithMinLevel(minLevel)})
		h.agentName = "test-agent"

		events := make([]lifecycleEvent, n)
		for i := range n {
			events[i] = genLifecycleEvent(rt, i)
		}

		// Fire all events.
		for _, evt := range events {
			evt.fire(h)
		}

		records := ch.getRecords()

		// Count how many events should have been emitted (level >= minLevel).
		var expectedCount int
		for _, evt := range events {
			if evt.expectedLevel >= minLevel {
				expectedCount++
			}
		}

		if len(records) != expectedCount {
			rt.Fatalf("with minLevel=%v: expected %d records, got %d (total events=%d)",
				minLevel, expectedCount, len(records), n)
		}

		// Verify no record is below minLevel.
		for i, r := range records {
			if r.Level < minLevel {
				rt.Fatalf("record %d (%s): level %v is below minLevel %v",
					i, r.Message, r.Level, minLevel)
			}
		}

		// Verify the emitted records match the expected events in order.
		recordIdx := 0
		for _, evt := range events {
			if evt.expectedLevel >= minLevel {
				if recordIdx >= len(records) {
					rt.Fatalf("ran out of records: expected event %q at record index %d", evt.name, recordIdx)
				}
				r := records[recordIdx]
				if r.Message != evt.name {
					rt.Fatalf("record %d: expected message %q, got %q", recordIdx, evt.name, r.Message)
				}
				if r.Level != evt.expectedLevel {
					rt.Fatalf("record %d (%s): expected level %v, got %v",
						recordIdx, r.Message, evt.expectedLevel, r.Level)
				}
				recordIdx++
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: Structured attribute presence
// ---------------------------------------------------------------------------

// Feature: logging-hook, Property 3: Structured attribute presence
//
// TestProperty_3_StructuredAttributePresence verifies that for random
// invoke/tool/provider events with random parameters, every log entry
// contains the expected structured attributes for its lifecycle point:
//   - tool.start/tool.end contain "tool.name"
//   - End events contain "duration_ms"
//   - provider_call.end contains "input_tokens", "output_tokens", "tool_call_count"
//   - invoke.start contains "agent.name", "model.id", "conversation_id", "max_iterations"
//   - invoke.end contains "input_tokens", "output_tokens", "duration_ms"
//   - memory.start/memory.end contain "operation", "conversation_id"
//   - guardrail.complete contains "direction", "blocked"
//   - max_iterations_exceeded contains "limit"
//
// **Validates: Requirements 4.7**
func TestProperty_3_StructuredAttributePresence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ch := &captureHandler{}
		h := newSlogHook([]Option{WithHandler(ch)})
		agentName := genString(rt, "agentName")
		h.agentName = agentName

		// Generate and fire a random event, then check its attributes.
		eventType := rapid.IntRange(0, 11).Draw(rt, "eventType")

		switch eventType {
		case 0: // InvokeStart
			modelID := genString(rt, "modelID")
			convID := genString(rt, "convID")
			maxIter := rapid.IntRange(1, 100).Draw(rt, "maxIter")
			h.OnInvokeStart(agent.InvokeSpanParams{
				ModelID:        modelID,
				ConversationID: convID,
				MaxIterations:  maxIter,
			})
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttr(rt, attrs, "agent.name", agentName)
			requireAttr(rt, attrs, "model.id", modelID)
			requireAttr(rt, attrs, "conversation_id", convID)
			requireAttrExists(rt, attrs, "max_iterations")

		case 1: // InvokeEnd
			err := genError(rt, "err")
			usage := genTokenUsage(rt, "usage")
			dur := genDuration(rt, "dur")
			h.OnInvokeEnd(err, usage, dur)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttrExists(rt, attrs, "duration_ms")
			requireAttrExists(rt, attrs, "input_tokens")
			requireAttrExists(rt, attrs, "output_tokens")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 2: // ProviderCallStart
			modelID := genString(rt, "modelID")
			h.OnProviderCallStart(modelID)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttr(rt, attrs, "model.id", modelID)

		case 3: // ProviderCallEnd
			err := genError(rt, "err")
			usage := genTokenUsage(rt, "usage")
			toolCount := rapid.IntRange(0, 20).Draw(rt, "toolCount")
			dur := genDuration(rt, "dur")
			h.OnProviderCallEnd(err, usage, toolCount, dur)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttrExists(rt, attrs, "duration_ms")
			requireAttrExists(rt, attrs, "input_tokens")
			requireAttrExists(rt, attrs, "output_tokens")
			requireAttrExists(rt, attrs, "tool_call_count")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 4: // ToolStart
			toolName := genString(rt, "toolName")
			h.OnToolStart(toolName)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttr(rt, attrs, "tool.name", toolName)

		case 5: // ToolEnd
			toolName := genString(rt, "toolName")
			err := genError(rt, "err")
			dur := genDuration(rt, "dur")
			h.OnToolEnd(toolName, err, dur)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttr(rt, attrs, "tool.name", toolName)
			requireAttrExists(rt, attrs, "duration_ms")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 6: // GuardrailComplete
			direction := rapid.SampledFrom([]string{"input", "output"}).Draw(rt, "dir")
			blocked := rapid.Bool().Draw(rt, "blocked")
			err := genError(rt, "err")
			h.OnGuardrailComplete(direction, blocked, err)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttr(rt, attrs, "direction", direction)
			requireAttrExists(rt, attrs, "blocked")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 7: // MemoryStart
			op := rapid.SampledFrom([]string{"load", "save"}).Draw(rt, "op")
			convID := genString(rt, "convID")
			h.OnMemoryStart(op, convID)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttr(rt, attrs, "operation", op)
			requireAttr(rt, attrs, "conversation_id", convID)

		case 8: // MemoryEnd
			op := rapid.SampledFrom([]string{"load", "save"}).Draw(rt, "op")
			convID := genString(rt, "convID")
			err := genError(rt, "err")
			msgCount := rapid.IntRange(0, 100).Draw(rt, "msgCount")
			dur := genDuration(rt, "dur")
			h.OnMemoryEnd(op, convID, err, msgCount, dur)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttr(rt, attrs, "operation", op)
			requireAttr(rt, attrs, "conversation_id", convID)
			requireAttrExists(rt, attrs, "message_count")
			requireAttrExists(rt, attrs, "duration_ms")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		case 9: // MaxIterationsExceeded
			limit := rapid.IntRange(1, 100).Draw(rt, "limit")
			h.OnMaxIterationsExceeded(limit)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttrExists(rt, attrs, "limit")

		case 10: // RetrieverEnd
			err := genError(rt, "err")
			docCount := rapid.IntRange(0, 100).Draw(rt, "docCount")
			dur := genDuration(rt, "dur")
			h.OnRetrieverEnd(err, docCount, dur)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttrExists(rt, attrs, "doc_count")
			requireAttrExists(rt, attrs, "duration_ms")
			if err != nil {
				requireAttrExists(rt, attrs, "error")
			}

		default: // IterationStart
			iter := rapid.IntRange(1, 100).Draw(rt, "iter")
			h.OnIterationStart(iter)
			records := ch.getRecords()
			if len(records) != 1 {
				rt.Fatalf("expected 1 record, got %d", len(records))
			}
			attrs := recordAttrs(records[0])
			requireAttrExists(rt, attrs, "iteration")
		}
	})
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

// requireAttr asserts that the attribute map contains the given key with the expected string value.
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
