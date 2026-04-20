package otel

import (
	"context"
	"errors"
	"fmt"
	"testing"

	agent "github.com/camilbinas/gude-agents/agent"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestHookPBT creates a fresh otelHook with its own ManualReader + MeterProvider
// for test isolation. Returns the hook and reader so tests can collect metrics.
func newTestHookPBT() (*otelHook, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	h := &otelHook{}
	h.meter = mp.Meter(defaultMeterName)
	if err := h.register(); err != nil {
		panic(fmt.Sprintf("register failed: %v", err))
	}
	return h, reader
}

// collectCounter collects metrics from the ManualReader and returns the counter
// value for the given metric name and attribute key-value pair. Returns 0 if not found.
func collectCounter(reader *sdkmetric.ManualReader, name string, attrs map[string]string) int64 {
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		return 0
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sumData, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sumData.DataPoints {
				if matchOTELAttrs(dp, attrs) {
					return dp.Value
				}
			}
		}
	}
	return 0
}

// matchOTELAttrs checks whether a data point's attributes match the given key-value pairs.
func matchOTELAttrs(dp metricdata.DataPoint[int64], want map[string]string) bool {
	// Build a map from the data point's attributes.
	got := make(map[string]string)
	iter := dp.Attributes.Iter()
	for iter.Next() {
		kv := iter.Attribute()
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Property 1: OTEL invoke counter correctness with status mapping
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 1: OTEL invoke counter correctness with status mapping
//
// TestProperty_OTELInvokeCounterCorrectness verifies that for any sequence of
// N invocations with random success/error outcomes, the OTEL agent.invoke.total
// counter with attribute status="success" equals the count of nil errors, and
// with attribute status="error" equals the count of non-nil errors, and the
// sum of both equals N.
//
// **Validates: Requirements 3.1, 5.3**
func TestProperty_OTELInvokeCounterCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		h, reader := newTestHookPBT()

		n := rapid.IntRange(1, 50).Draw(rt, "n")
		var successCount, errorCount int

		for i := range n {
			isErr := rapid.Bool().Draw(rt, fmt.Sprintf("err_%d", i))
			finish := h.OnInvokeStart()

			var err error
			if isErr {
				err = errors.New("fail")
				errorCount++
			} else {
				successCount++
			}
			finish(err, agent.TokenUsage{})
		}

		gotSuccess := collectCounter(reader, "agent.invoke.total", map[string]string{"status": "success"})
		gotError := collectCounter(reader, "agent.invoke.total", map[string]string{"status": "error"})

		if int(gotSuccess) != successCount {
			rt.Fatalf("success count: got %d, want %d", gotSuccess, successCount)
		}
		if int(gotError) != errorCount {
			rt.Fatalf("error count: got %d, want %d", gotError, errorCount)
		}
		if int(gotSuccess+gotError) != n {
			rt.Fatalf("total count: got %d, want %d", gotSuccess+gotError, n)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 2: OTEL provider metrics with model ID fallback
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 2: OTEL provider metrics with model ID fallback
//
// TestProperty_OTELProviderMetricsWithModelIDFallback verifies that for any
// sequence of provider call completions with random TokenUsage values
// (non-negative InputTokens and OutputTokens), random model ID strings
// (including empty), and random success/error outcomes:
//   - The provider call counter per (model_id, status) pair is correct
//   - The token counter per (model_id, direction) pair is correct (only for successful calls)
//   - The model_id attribute equals the input when non-empty, "unknown" when empty
//
// **Validates: Requirements 3.2, 3.3, 5.1**
func TestProperty_OTELProviderMetricsWithModelIDFallback(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		h, reader := newTestHookPBT()

		n := rapid.IntRange(1, 30).Draw(rt, "n")

		// Track expected values per (effectiveModelID, status) for call counter.
		type callKey struct {
			modelID string
			status  string
		}
		expectedCalls := make(map[callKey]int64)

		// Track expected token sums per (effectiveModelID, direction) for token counter.
		type tokenKey struct {
			modelID   string
			direction string
		}
		expectedTokens := make(map[tokenKey]int64)

		for i := range n {
			modelID := rapid.OneOf(
				rapid.Just(""),
				rapid.StringMatching("[a-zA-Z][a-zA-Z0-9_-]*"),
			).Draw(rt, fmt.Sprintf("model_%d", i))

			inputTokens := rapid.IntRange(0, 1000).Draw(rt, fmt.Sprintf("input_tokens_%d", i))
			outputTokens := rapid.IntRange(0, 1000).Draw(rt, fmt.Sprintf("output_tokens_%d", i))
			isErr := rapid.Bool().Draw(rt, fmt.Sprintf("err_%d", i))

			finish := h.OnProviderCallStart(modelID)

			// Determine effective model ID (the fallback logic).
			effectiveModelID := modelID
			if effectiveModelID == "" {
				effectiveModelID = "unknown"
			}

			var err error
			status := "success"
			if isErr {
				err = errors.New("fail")
				status = "error"
			}

			usage := agent.TokenUsage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			}
			finish(err, usage)

			// Update expected call counter.
			expectedCalls[callKey{effectiveModelID, status}]++

			// Token counters only increment on success.
			if err == nil {
				expectedTokens[tokenKey{effectiveModelID, "input"}] += int64(inputTokens)
				expectedTokens[tokenKey{effectiveModelID, "output"}] += int64(outputTokens)
			}
		}

		// Verify provider call counter per (model_id, status) pair.
		for key, want := range expectedCalls {
			got := collectCounter(reader, "agent.provider.call.total", map[string]string{
				"model_id": key.modelID,
				"status":   key.status,
			})
			if got != want {
				rt.Fatalf("provider call counter (model_id=%q, status=%q): got %d, want %d",
					key.modelID, key.status, got, want)
			}
		}

		// Verify token counter per (model_id, direction) pair.
		for key, want := range expectedTokens {
			got := collectCounter(reader, "agent.provider.tokens.total", map[string]string{
				"model_id":  key.modelID,
				"direction": key.direction,
			})
			if got != want {
				rt.Fatalf("token counter (model_id=%q, direction=%q): got %d, want %d",
					key.modelID, key.direction, got, want)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: OTEL tool call counter correctness
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 3: OTEL tool call counter correctness
//
// TestProperty_OTELToolCallCounterCorrectness verifies that for any sequence
// of tool executions with random tool names (from a fixed set) and random
// success/error outcomes, the OTEL agent.tool.call.total counter for each
// (tool_name, status) pair equals the count of calls with that tool name and
// outcome.
//
// **Validates: Requirements 3.4**
func TestProperty_OTELToolCallCounterCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		h, reader := newTestHookPBT()

		n := rapid.IntRange(1, 30).Draw(rt, "n")

		toolNames := []string{"search", "calculator", "fetch"}

		type toolKey struct {
			toolName string
			status   string
		}
		expected := make(map[toolKey]int64)

		for i := range n {
			toolName := rapid.SampledFrom(toolNames).Draw(rt, fmt.Sprintf("tool_%d", i))
			isErr := rapid.Bool().Draw(rt, fmt.Sprintf("err_%d", i))

			finish := h.OnToolStart(toolName)

			var err error
			status := "success"
			if isErr {
				err = errors.New("fail")
				status = "error"
			}
			finish(err)

			expected[toolKey{toolName, status}]++
		}

		// Verify tool call counter per (tool_name, status) pair.
		for key, want := range expected {
			got := collectCounter(reader, "agent.tool.call.total", map[string]string{
				"tool_name": key.toolName,
				"status":    key.status,
			})
			if got != want {
				rt.Fatalf("tool call counter (tool_name=%q, status=%q): got %d, want %d",
					key.toolName, key.status, got, want)
			}
		}

		// Verify total across all (tool_name, status) pairs equals n.
		var total int64
		for _, want := range expected {
			total += want
		}
		if total != int64(n) {
			rt.Fatalf("total tool calls: got %d, want %d", total, n)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: OTEL guardrail block counter selectivity
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 4: OTEL guardrail block counter selectivity
//
// TestProperty_OTELGuardrailBlockSelectivity verifies that for any sequence of
// guardrail completions with random direction ("input"/"output") and random
// blocked (true/false) values, the OTEL agent.guardrail.block.total counter
// for each direction SHALL equal the count of calls where blocked was true for
// that direction. Calls with blocked=false SHALL NOT increment the counter.
//
// **Validates: Requirements 3.5**
func TestProperty_OTELGuardrailBlockSelectivity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		h, reader := newTestHookPBT()

		n := rapid.IntRange(1, 50).Draw(rt, "n")

		// Track expected blocked counts per direction.
		expectedBlocked := map[string]int64{
			"input":  0,
			"output": 0,
		}

		for i := range n {
			direction := rapid.SampledFrom([]string{"input", "output"}).Draw(rt, fmt.Sprintf("dir_%d", i))
			blocked := rapid.Bool().Draw(rt, fmt.Sprintf("blocked_%d", i))

			h.OnGuardrailComplete(direction, blocked)

			if blocked {
				expectedBlocked[direction]++
			}
		}

		// Verify counter per direction matches only blocked=true calls.
		for _, dir := range []string{"input", "output"} {
			got := collectCounter(reader, "agent.guardrail.block.total", map[string]string{
				"direction": dir,
			})
			want := expectedBlocked[dir]
			if got != want {
				rt.Fatalf("guardrail block counter (direction=%q): got %d, want %d", dir, got, want)
			}
		}

		// Verify total blocked count equals sum of per-direction counts.
		totalBlocked := expectedBlocked["input"] + expectedBlocked["output"]
		gotInput := collectCounter(reader, "agent.guardrail.block.total", map[string]string{"direction": "input"})
		gotOutput := collectCounter(reader, "agent.guardrail.block.total", map[string]string{"direction": "output"})
		if gotInput+gotOutput != totalBlocked {
			rt.Fatalf("total guardrail blocks: got %d, want %d", gotInput+gotOutput, totalBlocked)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: OTEL iteration counter monotonicity
// ---------------------------------------------------------------------------

// Feature: metrics-exporters, Property 5: OTEL iteration counter monotonicity
//
// TestProperty_OTELIterationCounterMonotonicity verifies that for any
// non-negative integer N, calling OnIterationStart exactly N times results in
// the OTEL agent.iteration.total counter having value N.
//
// **Validates: Requirements 3.6**
func TestProperty_OTELIterationCounterMonotonicity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		h, reader := newTestHookPBT()

		n := rapid.IntRange(0, 100).Draw(rt, "n")

		for range n {
			h.OnIterationStart()
		}

		got := collectCounter(reader, "agent.iteration.total", map[string]string{})

		if got != int64(n) {
			rt.Fatalf("iteration counter: got %d, want %d", got, n)
		}
	})
}
