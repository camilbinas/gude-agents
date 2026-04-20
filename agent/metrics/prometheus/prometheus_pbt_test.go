package prometheus

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	agent "github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// gatherCounter reads the counter value for a given metric name and label set
// from a Prometheus registry. Returns 0 if the metric is not found.
func gatherCounter(reg *prom.Registry, name string, labels map[string]string) float64 {
	families, err := reg.Gather()
	if err != nil {
		return 0
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if matchLabels(m, labels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// gatherCounterTotal sums all counter values for a given metric name across
// all label combinations.
func gatherCounterTotal(reg *prom.Registry, name string) float64 {
	families, err := reg.Gather()
	if err != nil {
		return 0
	}
	var total float64
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

// matchLabels checks whether a dto.Metric has exactly the given label set.
func matchLabels(m *dto.Metric, labels map[string]string) bool {
	ml := m.GetLabel()
	if len(ml) != len(labels) {
		return false
	}
	for _, l := range ml {
		v, ok := labels[l.GetName()]
		if !ok || v != l.GetValue() {
			return false
		}
	}
	return true
}

// newTestHook creates a fresh prometheusHook with its own registry for test isolation.
func newTestHook(ns string) (*prometheusHook, *prom.Registry) {
	reg := prom.NewRegistry()
	h := &prometheusHook{
		namespace:  ns,
		registerer: reg,
		gatherer:   reg,
	}
	h.register()
	return h, reg
}

// ---------------------------------------------------------------------------
// Property 1: Status label mapping
// ---------------------------------------------------------------------------

// Feature: prometheus-metrics, Property 1: Status label mapping
//
// TestProperty_StatusLabelMapping verifies that for any error value (nil or
// non-nil), the statusLabel helper returns "success" for nil and "error" for
// non-nil.
//
// **Validates: Requirements 9.3**
func TestProperty_StatusLabelMapping(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		isErr := rapid.Bool().Draw(rt, "isError")

		var err error
		if isErr {
			msg := rapid.StringMatching("[a-zA-Z0-9 ]{1,50}").Draw(rt, "errMsg")
			err = errors.New(msg)
		}

		got := statusLabel(err)

		if err == nil && got != "success" {
			rt.Fatalf("statusLabel(nil) = %q, want \"success\"", got)
		}
		if err != nil && got != "error" {
			rt.Fatalf("statusLabel(non-nil) = %q, want \"error\"", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 2: Model ID fallback
// ---------------------------------------------------------------------------

// Feature: prometheus-metrics, Property 2: Model ID fallback
//
// TestProperty_ModelIDFallback verifies that for any string passed as modelID
// to OnProviderCallStart, the label recorded on provider metrics equals the
// input when non-empty, and equals "unknown" when the input is empty.
//
// **Validates: Requirements 9.1**
func TestProperty_ModelIDFallback(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a string that may be empty.
		modelID := rapid.OneOf(
			rapid.Just(""),
			rapid.StringMatching("[a-zA-Z][a-zA-Z0-9_-]*"),
		).Draw(rt, "modelID")

		h, reg := newTestHook("")

		finish := h.OnProviderCallStart(modelID)
		finish(nil, agent.TokenUsage{InputTokens: 1, OutputTokens: 1})

		expectedLabel := modelID
		if modelID == "" {
			expectedLabel = "unknown"
		}

		val := gatherCounter(reg, "agent_provider_call_total", map[string]string{
			"model_id": expectedLabel,
			"status":   "success",
		})
		if val != 1 {
			rt.Fatalf("expected counter=1 for model_id=%q, got %v", expectedLabel, val)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: Invoke counter correctness
// ---------------------------------------------------------------------------

// Feature: prometheus-metrics, Property 3: Invoke counter correctness
//
// TestProperty_InvokeCounterCorrectness verifies that for any sequence of N
// invocations with random success/error outcomes, the invoke counter sums
// match the expected counts.
//
// **Validates: Requirements 4.1**
func TestProperty_InvokeCounterCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(rt, "n")
		h, reg := newTestHook("")

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

		gotSuccess := gatherCounter(reg, "agent_invoke_total", map[string]string{"status": "success"})
		gotError := gatherCounter(reg, "agent_invoke_total", map[string]string{"status": "error"})

		if int(gotSuccess) != successCount {
			rt.Fatalf("success count: got %v, want %d", gotSuccess, successCount)
		}
		if int(gotError) != errorCount {
			rt.Fatalf("error count: got %v, want %d", gotError, errorCount)
		}
		if int(gotSuccess+gotError) != n {
			rt.Fatalf("total count: got %v, want %d", gotSuccess+gotError, n)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: Provider token accounting
// ---------------------------------------------------------------------------

// Feature: prometheus-metrics, Property 4: Provider token accounting
//
// TestProperty_ProviderTokenAccounting verifies that for any sequence of
// provider call completions with random TokenUsage values and model IDs,
// token counters sum correctly per model and direction.
//
// **Validates: Requirements 4.2, 4.3**
func TestProperty_ProviderTokenAccounting(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 30).Draw(rt, "n")
		h, reg := newTestHook("")

		// Track expected sums per model per direction.
		type key struct {
			model, direction string
		}
		expected := make(map[key]float64)

		for i := range n {
			modelID := rapid.SampledFrom([]string{"modelA", "modelB", "modelC"}).
				Draw(rt, fmt.Sprintf("model_%d", i))
			inputTokens := rapid.IntRange(0, 1000).Draw(rt, fmt.Sprintf("input_%d", i))
			outputTokens := rapid.IntRange(0, 1000).Draw(rt, fmt.Sprintf("output_%d", i))

			finish := h.OnProviderCallStart(modelID)
			finish(nil, agent.TokenUsage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
			})

			expected[key{modelID, "input"}] += float64(inputTokens)
			expected[key{modelID, "output"}] += float64(outputTokens)
		}

		for k, want := range expected {
			got := gatherCounter(reg, "agent_provider_tokens_total", map[string]string{
				"model_id":  k.model,
				"direction": k.direction,
			})
			if got != want {
				rt.Fatalf("tokens for model=%q direction=%q: got %v, want %v",
					k.model, k.direction, got, want)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Tool call counter correctness
// ---------------------------------------------------------------------------

// Feature: prometheus-metrics, Property 5: Tool call counter correctness
//
// TestProperty_ToolCallCounterCorrectness verifies that for any sequence of
// tool executions with random tool names and outcomes, counters match per
// (tool_name, status) pair.
//
// **Validates: Requirements 4.4**
func TestProperty_ToolCallCounterCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 30).Draw(rt, "n")
		h, reg := newTestHook("")

		type key struct {
			tool, status string
		}
		expected := make(map[key]float64)

		for i := range n {
			toolName := rapid.SampledFrom([]string{"search", "calculator", "fetch"}).
				Draw(rt, fmt.Sprintf("tool_%d", i))
			isErr := rapid.Bool().Draw(rt, fmt.Sprintf("err_%d", i))

			finish := h.OnToolStart(toolName)

			var err error
			status := "success"
			if isErr {
				err = errors.New("tool failed")
				status = "error"
			}
			finish(err)

			expected[key{toolName, status}]++
		}

		for k, want := range expected {
			got := gatherCounter(reg, "agent_tool_call_total", map[string]string{
				"tool_name": k.tool,
				"status":    k.status,
			})
			if got != want {
				rt.Fatalf("tool_call_total for tool=%q status=%q: got %v, want %v",
					k.tool, k.status, got, want)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 6: Guardrail block counter selectivity
// ---------------------------------------------------------------------------

// Feature: prometheus-metrics, Property 6: Guardrail block counter selectivity
//
// TestProperty_GuardrailBlockSelectivity verifies that for any sequence of
// guardrail completions with random direction and blocked values, the counter
// only increments when blocked is true.
//
// **Validates: Requirements 4.5**
func TestProperty_GuardrailBlockSelectivity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(rt, "n")
		h, reg := newTestHook("")

		expected := map[string]float64{
			"input":  0,
			"output": 0,
		}

		for i := range n {
			direction := rapid.SampledFrom([]string{"input", "output"}).
				Draw(rt, fmt.Sprintf("dir_%d", i))
			blocked := rapid.Bool().Draw(rt, fmt.Sprintf("blocked_%d", i))

			h.OnGuardrailComplete(direction, blocked)

			if blocked {
				expected[direction]++
			}
		}

		for dir, want := range expected {
			got := gatherCounter(reg, "agent_guardrail_block_total", map[string]string{
				"direction": dir,
			})
			if got != want {
				rt.Fatalf("guardrail_block_total for direction=%q: got %v, want %v",
					dir, got, want)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 7: Iteration counter monotonicity
// ---------------------------------------------------------------------------

// Feature: prometheus-metrics, Property 7: Iteration counter monotonicity
//
// TestProperty_IterationCounterMonotonicity verifies that for any non-negative
// integer N, calling OnIterationStart exactly N times results in the counter
// having value N.
//
// **Validates: Requirements 4.6**
func TestProperty_IterationCounterMonotonicity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 100).Draw(rt, "n")
		h, reg := newTestHook("")

		for range n {
			h.OnIterationStart()
		}

		got := gatherCounterTotal(reg, "agent_iteration_total")
		if int(got) != n {
			rt.Fatalf("iteration_total: got %v, want %d", got, n)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 8: Namespace prefixing
// ---------------------------------------------------------------------------

// Feature: prometheus-metrics, Property 8: Namespace prefixing
//
// TestProperty_NamespacePrefixing verifies that for any non-empty namespace
// string, all registered metric names are prefixed with that namespace
// followed by an underscore. When the namespace is empty, metric names have
// no prefix.
//
// **Validates: Requirements 8.4**
func TestProperty_NamespacePrefixing(t *testing.T) {
	baseNames := []string{
		"agent_invoke_total",
		"agent_invoke_duration_seconds",
		"agent_provider_call_total",
		"agent_provider_call_duration_seconds",
		"agent_provider_tokens_total",
		"agent_tool_call_total",
		"agent_tool_call_duration_seconds",
		"agent_guardrail_block_total",
		"agent_iteration_total",
	}

	rapid.Check(t, func(rt *rapid.T) {
		ns := rapid.StringMatching("[a-z][a-z0-9_]*").Draw(rt, "namespace")

		h, reg := newTestHook(ns)

		// Exercise hooks so all metric families appear in Gather output.
		finishInvoke := h.OnInvokeStart()
		finishInvoke(nil, agent.TokenUsage{InputTokens: 1, OutputTokens: 1})
		h.OnIterationStart()
		finishProvider := h.OnProviderCallStart("m")
		finishProvider(nil, agent.TokenUsage{InputTokens: 1, OutputTokens: 1})
		finishTool := h.OnToolStart("t")
		finishTool(nil)
		h.OnGuardrailComplete("input", true)

		families, err := reg.Gather()
		if err != nil {
			rt.Fatalf("gather error: %v", err)
		}

		gathered := make(map[string]bool)
		for _, f := range families {
			gathered[f.GetName()] = true
		}

		for _, base := range baseNames {
			var want string
			if ns != "" {
				want = ns + "_" + base
			} else {
				want = base
			}
			if !gathered[want] {
				rt.Fatalf("expected metric %q in registry, got names: %v",
					want, mapKeys(gathered))
			}
		}

		// Verify no metric name lacks the prefix when namespace is set.
		if ns != "" {
			prefix := ns + "_"
			for name := range gathered {
				if !strings.HasPrefix(name, prefix) {
					rt.Fatalf("metric %q does not have expected prefix %q", name, prefix)
				}
			}
		}
	})
}

// mapKeys returns the keys of a map as a sorted-ish slice for error messages.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
