package prometheus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// recordingObserver verifies that independent observers coexist with metrics.
type recordingObserver struct {
	mu              sync.Mutex
	invokeCalled    bool
	iterationCalled bool
	providerCalled  bool
	toolCalled      bool
	guardrailCalled bool
}

func (h *recordingObserver) ObserveInvoke(ctx context.Context, record agent.InvokeRecord) context.Context {
	if record.Phase == agent.Start {
		h.mu.Lock()
		h.invokeCalled = true
		h.mu.Unlock()
	}
	return ctx
}

func (h *recordingObserver) ObserveIteration(ctx context.Context, record agent.IterationRecord) context.Context {
	if record.Phase == agent.Start {
		h.mu.Lock()
		h.iterationCalled = true
		h.mu.Unlock()
	}
	return ctx
}

func (h *recordingObserver) ObserveModel(ctx context.Context, record agent.ModelCallRecord) context.Context {
	if record.Phase == agent.Start {
		h.mu.Lock()
		h.providerCalled = true
		h.mu.Unlock()
	}
	return ctx
}

func (h *recordingObserver) ObserveTool(ctx context.Context, record agent.ToolCallRecord) context.Context {
	if record.Phase == agent.Start {
		h.mu.Lock()
		h.toolCalled = true
		h.mu.Unlock()
	}
	return ctx
}

func (h *recordingObserver) ObserveGuardrail(ctx context.Context, record agent.GuardrailRecord) context.Context {
	if record.Phase == agent.Start {
		h.mu.Lock()
		h.guardrailCalled = true
		h.mu.Unlock()
	}
	return ctx
}

// ---------------------------------------------------------------------------
// Unit Tests
// ---------------------------------------------------------------------------

// TestWithMetrics_RegistersObserver verifies that WithMetrics installs a working observer.
func TestWithMetrics_RegistersObserver(t *testing.T) {
	reg := prom.NewRegistry()
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "hello"}))

	a, err := agent.New(prov, "sys",
		WithMetrics(WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a == nil {
		t.Fatal("expected agent construction to succeed")
	}
}

// TestWithMetrics_CustomRegisterer verifies that a custom registerer receives metrics.
func TestWithMetrics_CustomRegisterer(t *testing.T) {
	reg := prom.NewRegistry()
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "hello"}))

	a, err := agent.New(prov, "sys",
		agent.WithName("metrics-agent"),
		WithMetrics(WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Trigger an invocation so counters get label values and appear in Gather.
	_, err = a.Invoke(agent.Background(), "hi")
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}

	// Gather metrics from the custom registry — it should have our metrics.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	if len(families) == 0 {
		t.Fatal("expected metrics to be registered in custom registry, got 0 families")
	}

	// Check that our metric names are present.
	names := make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}

	for _, want := range []string{"agent_invoke_total", "agent_iteration_total"} {
		if !names[want] {
			t.Errorf("expected %q in custom registry, got families: %v", want, names)
		}
	}

	if got := gatherCounter(reg, "agent_invoke_total", map[string]string{
		"agent_name": "metrics-agent", "status": "success",
	}); got != 1 {
		t.Errorf("named agent invoke counter = %v, want 1", got)
	}
}

// TestHandler_ServesMetrics verifies the HTTP handler returns Prometheus exposition
// format with all 9 metric names.
func TestHandler_ServesMetrics(t *testing.T) {
	reg := prom.NewRegistry()

	h := &prometheusHook{
		registerer: reg,
		gatherer:   reg,
	}
	h.register()

	// Exercise every hook method so all metric families appear in the output.
	_ = h.ObserveInvoke(context.Background(), agent.InvokeRecord{
		Phase: agent.End, Duration: time.Second,
	})

	_ = h.ObserveIteration(context.Background(), agent.IterationRecord{Phase: agent.Start})

	_ = h.ObserveModel(context.Background(), agent.ModelCallRecord{
		Phase: agent.End, ModelID: "test-model", Duration: time.Second,
		Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
	})

	_ = h.ObserveTool(context.Background(), agent.ToolCallRecord{
		Phase: agent.End, Name: "my-tool", Duration: time.Second,
	})

	_ = h.ObserveGuardrail(context.Background(), agent.GuardrailRecord{
		Phase: agent.End, Direction: "input", Blocked: true,
	})
	_ = h.ObserveAttachment(context.Background(), agent.AttachmentRecord{
		Phase: agent.End, ImageCount: 2, DocumentCount: 3,
	})

	handler := h.Handler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	bodyStr := string(body)

	expectedMetrics := []string{
		"agent_invoke_total",
		"agent_invoke_duration_seconds",
		"agent_provider_call_total",
		"agent_provider_call_duration_seconds",
		"agent_provider_tokens_total",
		"agent_tool_call_total",
		"agent_tool_call_duration_seconds",
		"agent_guardrail_block_total",
		"agent_iteration_total",
		"agent_images_attached_total",
		"agent_documents_attached_total",
	}

	for _, name := range expectedMetrics {
		if !strings.Contains(bodyStr, name) {
			t.Errorf("expected metric %q in handler response, not found", name)
		}
	}
}

// TestHistogramBuckets verifies histogram buckets match the LLM latency spec.
func TestHistogramBuckets(t *testing.T) {
	expected := []float64{0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0}

	if len(llmBuckets) != len(expected) {
		t.Fatalf("expected %d buckets, got %d", len(expected), len(llmBuckets))
	}

	for i, want := range expected {
		if llmBuckets[i] != want {
			t.Errorf("bucket[%d]: expected %v, got %v", i, want, llmBuckets[i])
		}
	}
}

// TestDurationRecording verifies histograms record non-negative durations.
func TestDurationRecording(t *testing.T) {
	reg := prom.NewRegistry()

	h := &prometheusHook{
		registerer: reg,
		gatherer:   reg,
	}
	h.register()

	// Exercise all duration-recording hooks.
	_ = h.ObserveInvoke(context.Background(), agent.InvokeRecord{
		Phase: agent.End, Duration: 2 * time.Second,
	})

	_ = h.ObserveModel(context.Background(), agent.ModelCallRecord{
		Phase: agent.End, ModelID: "test-model", Duration: 3 * time.Second,
		Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
	})

	_ = h.ObserveTool(context.Background(), agent.ToolCallRecord{
		Phase: agent.End, Name: "my-tool", Duration: 4 * time.Second,
	})

	// Gather and verify all histograms have non-negative observations.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	histogramNames := map[string]bool{
		"agent_invoke_duration_seconds":        false,
		"agent_provider_call_duration_seconds": false,
		"agent_tool_call_duration_seconds":     false,
	}

	for _, f := range families {
		if _, ok := histogramNames[f.GetName()]; ok {
			histogramNames[f.GetName()] = true
			for _, m := range f.GetMetric() {
				hist := m.GetHistogram()
				if hist == nil {
					t.Errorf("metric %q: expected histogram, got nil", f.GetName())
					continue
				}
				sum := hist.GetSampleSum()
				if sum < 0 {
					t.Errorf("metric %q: expected non-negative sum, got %v", f.GetName(), sum)
				}
				count := hist.GetSampleCount()
				if count != 1 {
					t.Errorf("metric %q: expected sample count 1, got %d", f.GetName(), count)
				}
			}
		}
	}

	for name, found := range histogramNames {
		if !found {
			t.Errorf("histogram %q not found in gathered metrics", name)
		}
	}
}

// TestNoObserverNoPanic verifies invocation works without metrics observers.
func TestNoObserverNoPanic(t *testing.T) {
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "hello"}))

	a, err := agent.New(prov, "sys")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// This should not panic.
	result, err := a.Invoke(agent.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "hello" {
		t.Errorf("expected %q, got %q", "hello", result.Text)
	}
}

// TestCoexistenceWithTracing verifies both hooks receive callbacks when both are set.
func TestCoexistenceWithTracing(t *testing.T) {
	reg := prom.NewRegistry()
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "hello"}))

	observer := &recordingObserver{}

	a, err := agent.New(prov, "sys",
		WithMetrics(WithRegisterer(reg)),
		agent.WithObserver(observer),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = a.Invoke(agent.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify tracing hook received callbacks.
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if !observer.invokeCalled {
		t.Error("expected observer.OnInvokeStart to be called")
	}
	if !observer.iterationCalled {
		t.Error("expected observer.OnIterationStart to be called")
	}
	if !observer.providerCalled {
		t.Error("expected observer.OnProviderCallStart to be called")
	}

	// Verify metrics observer also received callbacks by checking the registry.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	metricsFound := map[string]bool{
		"agent_invoke_total":    false,
		"agent_iteration_total": false,
	}
	for _, f := range families {
		if _, ok := metricsFound[f.GetName()]; ok {
			for _, m := range f.GetMetric() {
				if getCounterValue(m) > 0 {
					metricsFound[f.GetName()] = true
				}
			}
		}
	}

	for name, found := range metricsFound {
		if !found {
			t.Errorf("expected metric %q to have been incremented (metrics observer active alongside tracing hook)", name)
		}
	}
}

// getCounterValue extracts the counter value from a prometheus Metric.
func getCounterValue(m *dto.Metric) float64 {
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	return 0
}

// ---------------------------------------------------------------------------
// Integration Tests
// ---------------------------------------------------------------------------

// TestAgentLoop_MetricsObserverCalled runs a full agent loop with a mock provider
// that returns a tool call followed by a text response, and verifies all
// Prometheus metrics are recorded at the correct lifecycle points.
func TestAgentLoop_MetricsObserverCalled(t *testing.T) {
	reg := prom.NewRegistry()

	// Mock provider: first response triggers a tool call, second is the final text.
	prov := testutil.NewMockProvider(testutil.WithResponses(
		&agent.ModelResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "call-1", Name: "my-tool", Input: json.RawMessage(`{}`)},
			},
			Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
		&agent.ModelResponse{
			Text:  "done",
			Usage: agent.TokenUsage{InputTokens: 20, OutputTokens: 10},
		},
	))

	// Register a simple tool that the mock provider will invoke.
	myTool := tool.NewRaw("my-tool", "A test tool",
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "result", nil
		})

	a, err := agent.New(prov, "sys",
		agent.WithTools(myTool),
		WithMetrics(WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("unexpected error creating agent: %v", err)
	}

	result, err := a.Invoke(agent.Background(), "do something")
	if err != nil {
		t.Fatalf("unexpected invoke error: %v", err)
	}
	if result.Text != "done" {
		t.Errorf("expected result %q, got %q", "done", result.Text)
	}

	// Gather all metrics from the registry.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	// Build a lookup map for metric families.
	familyMap := make(map[string]*dto.MetricFamily)
	for _, f := range families {
		familyMap[f.GetName()] = f
	}

	// Helper: sum all counter values across label combinations for a metric family.
	sumCounter := func(name string) float64 {
		f, ok := familyMap[name]
		if !ok {
			t.Errorf("metric %q not found in registry", name)
			return 0
		}
		var total float64
		for _, m := range f.GetMetric() {
			if c := m.GetCounter(); c != nil {
				total += c.GetValue()
			}
		}
		return total
	}

	// Helper: sum all histogram sample counts across label combinations.
	sumHistogramCount := func(name string) uint64 {
		f, ok := familyMap[name]
		if !ok {
			t.Errorf("metric %q not found in registry", name)
			return 0
		}
		var total uint64
		for _, m := range f.GetMetric() {
			if h := m.GetHistogram(); h != nil {
				total += h.GetSampleCount()
			}
		}
		return total
	}

	// Verify counters.
	// agent_invoke_total: 1 invocation (success).
	if v := sumCounter("agent_invoke_total"); v != 1 {
		t.Errorf("agent_invoke_total: expected 1, got %v", v)
	}

	// agent_iteration_total: at least 2 iterations (tool call + final response).
	if v := sumCounter("agent_iteration_total"); v < 2 {
		t.Errorf("agent_iteration_total: expected >= 2, got %v", v)
	}

	// agent_provider_call_total: at least 2 provider calls.
	if v := sumCounter("agent_provider_call_total"); v < 2 {
		t.Errorf("agent_provider_call_total: expected >= 2, got %v", v)
	}

	// agent_provider_tokens_total: should have input + output tokens recorded.
	if v := sumCounter("agent_provider_tokens_total"); v <= 0 {
		t.Errorf("agent_provider_tokens_total: expected > 0, got %v", v)
	}

	// agent_tool_call_total: 1 tool call (success).
	if v := sumCounter("agent_tool_call_total"); v != 1 {
		t.Errorf("agent_tool_call_total: expected 1, got %v", v)
	}

	// Verify histograms have observations.
	if v := sumHistogramCount("agent_invoke_duration_seconds"); v < 1 {
		t.Errorf("agent_invoke_duration_seconds: expected >= 1 observation, got %d", v)
	}

	if v := sumHistogramCount("agent_provider_call_duration_seconds"); v < 2 {
		t.Errorf("agent_provider_call_duration_seconds: expected >= 2 observations, got %d", v)
	}

	if v := sumHistogramCount("agent_tool_call_duration_seconds"); v < 1 {
		t.Errorf("agent_tool_call_duration_seconds: expected >= 1 observation, got %d", v)
	}
}

// TestAgentLoop_BothHooksActive verifies that both the tracing hook and the
// metrics observer fire independently during a full agent loop.
func TestAgentLoop_BothHooksActive(t *testing.T) {
	reg := prom.NewRegistry()

	// Mock provider: tool call then final text (exercises the full loop).
	prov := testutil.NewMockProvider(testutil.WithResponses(
		&agent.ModelResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "call-1", Name: "my-tool", Input: json.RawMessage(`{}`)},
			},
			Usage: agent.TokenUsage{InputTokens: 5, OutputTokens: 3},
		},
		&agent.ModelResponse{
			Text:  "all done",
			Usage: agent.TokenUsage{InputTokens: 8, OutputTokens: 4},
		},
	))

	myTool := tool.NewRaw("my-tool", "A test tool",
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "result", nil
		})

	observer := &recordingObserver{}

	a, err := agent.New(prov, "sys",
		agent.WithTools(myTool),
		WithMetrics(WithRegisterer(reg)),
		agent.WithObserver(observer),
	)
	if err != nil {
		t.Fatalf("unexpected error creating agent: %v", err)
	}

	result, err := a.Invoke(agent.Background(), "do something")
	if err != nil {
		t.Fatalf("unexpected invoke error: %v", err)
	}
	if result.Text != "all done" {
		t.Errorf("expected result %q, got %q", "all done", result.Text)
	}

	// Verify tracing hook received all expected callbacks.
	observer.mu.Lock()
	defer observer.mu.Unlock()

	if !observer.invokeCalled {
		t.Error("expected observer.OnInvokeStart to be called")
	}
	if !observer.iterationCalled {
		t.Error("expected observer.OnIterationStart to be called")
	}
	if !observer.providerCalled {
		t.Error("expected observer.OnProviderCallStart to be called")
	}
	if !observer.toolCalled {
		t.Error("expected observer.OnToolStart to be called")
	}

	// Verify metrics observer also recorded data by checking the registry.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	familyMap := make(map[string]*dto.MetricFamily)
	for _, f := range families {
		familyMap[f.GetName()] = f
	}

	// Check key metrics are present and have been incremented.
	expectedCounters := []string{
		"agent_invoke_total",
		"agent_iteration_total",
		"agent_provider_call_total",
		"agent_provider_tokens_total",
		"agent_tool_call_total",
	}

	for _, name := range expectedCounters {
		f, ok := familyMap[name]
		if !ok {
			t.Errorf("metric %q not found — metrics observer may not have fired", name)
			continue
		}
		var total float64
		for _, m := range f.GetMetric() {
			if c := m.GetCounter(); c != nil {
				total += c.GetValue()
			}
		}
		if total <= 0 {
			t.Errorf("metric %q has value %v — expected > 0 (metrics observer should have incremented it)", name, total)
		}
	}

	// Check histograms have observations.
	expectedHistograms := []string{
		"agent_invoke_duration_seconds",
		"agent_provider_call_duration_seconds",
		"agent_tool_call_duration_seconds",
	}

	for _, name := range expectedHistograms {
		f, ok := familyMap[name]
		if !ok {
			t.Errorf("histogram %q not found — metrics observer may not have fired", name)
			continue
		}
		var totalCount uint64
		for _, m := range f.GetMetric() {
			if h := m.GetHistogram(); h != nil {
				totalCount += h.GetSampleCount()
			}
		}
		if totalCount == 0 {
			t.Errorf("histogram %q has 0 observations — expected > 0", name)
		}
	}
}
