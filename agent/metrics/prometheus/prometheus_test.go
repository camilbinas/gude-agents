package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// mockProvider is a minimal Provider that returns pre-configured responses.
type mockProvider struct {
	mu        sync.Mutex
	responses []*agent.ProviderResponse
	callIndex int
}

func newMockProvider(responses ...*agent.ProviderResponse) *mockProvider {
	return &mockProvider{responses: responses}
}

func (p *mockProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	return p.ConverseStream(ctx, params, nil)
}

func (p *mockProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.callIndex >= len(p.responses) {
		return nil, fmt.Errorf("mockProvider: no more responses (call %d)", p.callIndex)
	}
	resp := p.responses[p.callIndex]
	p.callIndex++
	if len(resp.ToolCalls) == 0 && resp.Text != "" && cb != nil {
		cb(resp.Text)
	}
	return resp, nil
}

// mockTracingHook is a minimal TracingHook that records which callbacks were invoked.
type mockTracingHook struct {
	mu              sync.Mutex
	invokeCalled    bool
	iterationCalled bool
	providerCalled  bool
	toolCalled      bool
	guardrailCalled bool
}

func (h *mockTracingHook) OnInvokeStart(ctx context.Context, _ agent.InvokeSpanParams) (context.Context, func(error, agent.TokenUsage, string)) {
	h.mu.Lock()
	h.invokeCalled = true
	h.mu.Unlock()
	return ctx, func(_ error, _ agent.TokenUsage, _ string) {}
}

func (h *mockTracingHook) OnIterationStart(ctx context.Context, _ int) (context.Context, func(int, bool)) {
	h.mu.Lock()
	h.iterationCalled = true
	h.mu.Unlock()
	return ctx, func(_ int, _ bool) {}
}

func (h *mockTracingHook) OnProviderCallStart(ctx context.Context, _ agent.ProviderCallParams) (context.Context, func(error, agent.TokenUsage, int, string)) {
	h.mu.Lock()
	h.providerCalled = true
	h.mu.Unlock()
	return ctx, func(_ error, _ agent.TokenUsage, _ int, _ string) {}
}

func (h *mockTracingHook) OnToolStart(ctx context.Context, _ string, _ json.RawMessage) (context.Context, func(error, string)) {
	h.mu.Lock()
	h.toolCalled = true
	h.mu.Unlock()
	return ctx, func(_ error, _ string) {}
}

func (h *mockTracingHook) OnGuardrailStart(ctx context.Context, _ string, _ string) (context.Context, func(error, string)) {
	h.mu.Lock()
	h.guardrailCalled = true
	h.mu.Unlock()
	return ctx, func(_ error, _ string) {}
}

func (h *mockTracingHook) OnMemoryStart(ctx context.Context, _ string, _ string) (context.Context, func(error)) {
	return ctx, func(_ error) {}
}

func (h *mockTracingHook) OnRetrieverStart(ctx context.Context, _ string) (context.Context, func(error, int)) {
	return ctx, func(_ error, _ int) {}
}

func (h *mockTracingHook) OnMaxIterationsExceeded(_ context.Context, _ int) {}

// ---------------------------------------------------------------------------
// Unit Tests
// ---------------------------------------------------------------------------

// TestWithMetrics_InstallsHook verifies that WithMetrics sets MetricsHook on the agent.
func TestWithMetrics_InstallsHook(t *testing.T) {
	reg := prom.NewRegistry()
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})

	a, err := agent.New(prov, prompt.Text("sys"), nil,
		WithMetrics(WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.MetricsHook() == nil {
		t.Fatal("expected MetricsHook to be set after WithMetrics, got nil")
	}
}

// TestWithMetrics_CustomRegisterer verifies that a custom registerer receives metrics.
func TestWithMetrics_CustomRegisterer(t *testing.T) {
	reg := prom.NewRegistry()
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})

	a, err := agent.New(prov, prompt.Text("sys"), nil,
		WithMetrics(WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Trigger an invocation so counters get label values and appear in Gather.
	_, _, err = a.Invoke(context.Background(), "hi")
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
	finishInvoke := h.OnInvokeStart()
	finishInvoke(nil, agent.TokenUsage{InputTokens: 10, OutputTokens: 5})

	h.OnIterationStart()

	finishProvider := h.OnProviderCallStart("test-model")
	finishProvider(nil, agent.TokenUsage{InputTokens: 10, OutputTokens: 5})

	finishTool := h.OnToolStart("my-tool")
	finishTool(nil)

	h.OnGuardrailComplete("input", true)

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
	finishInvoke := h.OnInvokeStart()
	finishInvoke(nil, agent.TokenUsage{})

	finishProvider := h.OnProviderCallStart("test-model")
	finishProvider(nil, agent.TokenUsage{InputTokens: 10, OutputTokens: 5})

	finishTool := h.OnToolStart("my-tool")
	finishTool(nil)

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

// TestNilHookNoPanic verifies that an agent with nil MetricsHook doesn't panic
// during invocation.
func TestNilHookNoPanic(t *testing.T) {
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})

	// Create agent without WithMetrics — MetricsHook should be nil.
	a, err := agent.New(prov, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.MetricsHook() != nil {
		t.Fatal("expected MetricsHook to be nil without WithMetrics")
	}

	// This should not panic.
	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected %q, got %q", "hello", result)
	}
}

// TestCoexistenceWithTracing verifies both hooks receive callbacks when both are set.
func TestCoexistenceWithTracing(t *testing.T) {
	reg := prom.NewRegistry()
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})

	tracingHook := &mockTracingHook{}

	a, err := agent.New(prov, prompt.Text("sys"), nil,
		WithMetrics(WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Manually set the tracing hook (since we don't want to pull in the
	// full tracing submodule dependency).
	a.SetTracingHook(tracingHook)

	_, _, err = a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify tracing hook received callbacks.
	tracingHook.mu.Lock()
	defer tracingHook.mu.Unlock()
	if !tracingHook.invokeCalled {
		t.Error("expected TracingHook.OnInvokeStart to be called")
	}
	if !tracingHook.iterationCalled {
		t.Error("expected TracingHook.OnIterationStart to be called")
	}
	if !tracingHook.providerCalled {
		t.Error("expected TracingHook.OnProviderCallStart to be called")
	}

	// Verify metrics hook also received callbacks by checking the registry.
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
			t.Errorf("expected metric %q to have been incremented (metrics hook active alongside tracing hook)", name)
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
// Integration Tests (Task 7.1)
// ---------------------------------------------------------------------------

// TestAgentLoop_MetricsHookCalled runs a full agent loop with a mock provider
// that returns a tool call followed by a text response, and verifies all
// Prometheus metrics are recorded at the correct lifecycle points.
func TestAgentLoop_MetricsHookCalled(t *testing.T) {
	reg := prom.NewRegistry()

	// Mock provider: first response triggers a tool call, second is the final text.
	prov := newMockProvider(
		&agent.ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "call-1", Name: "my-tool", Input: json.RawMessage(`{}`)},
			},
			Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
		&agent.ProviderResponse{
			Text:  "done",
			Usage: agent.TokenUsage{InputTokens: 20, OutputTokens: 10},
		},
	)

	// Register a simple tool that the mock provider will invoke.
	myTool := tool.NewRaw("my-tool", "A test tool", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "result", nil
		})

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{myTool},
		WithMetrics(WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("unexpected error creating agent: %v", err)
	}

	result, _, err := a.Invoke(context.Background(), "do something")
	if err != nil {
		t.Fatalf("unexpected invoke error: %v", err)
	}
	if result != "done" {
		t.Errorf("expected result %q, got %q", "done", result)
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
// metrics hook fire independently during a full agent loop.
func TestAgentLoop_BothHooksActive(t *testing.T) {
	reg := prom.NewRegistry()

	// Mock provider: tool call then final text (exercises the full loop).
	prov := newMockProvider(
		&agent.ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "call-1", Name: "my-tool", Input: json.RawMessage(`{}`)},
			},
			Usage: agent.TokenUsage{InputTokens: 5, OutputTokens: 3},
		},
		&agent.ProviderResponse{
			Text:  "all done",
			Usage: agent.TokenUsage{InputTokens: 8, OutputTokens: 4},
		},
	)

	myTool := tool.NewRaw("my-tool", "A test tool", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "result", nil
		})

	tracingHook := &mockTracingHook{}

	a, err := agent.New(prov, prompt.Text("sys"), []tool.Tool{myTool},
		WithMetrics(WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("unexpected error creating agent: %v", err)
	}

	// Set the tracing hook alongside the metrics hook.
	a.SetTracingHook(tracingHook)

	result, _, err := a.Invoke(context.Background(), "do something")
	if err != nil {
		t.Fatalf("unexpected invoke error: %v", err)
	}
	if result != "all done" {
		t.Errorf("expected result %q, got %q", "all done", result)
	}

	// Verify tracing hook received all expected callbacks.
	tracingHook.mu.Lock()
	defer tracingHook.mu.Unlock()

	if !tracingHook.invokeCalled {
		t.Error("expected TracingHook.OnInvokeStart to be called")
	}
	if !tracingHook.iterationCalled {
		t.Error("expected TracingHook.OnIterationStart to be called")
	}
	if !tracingHook.providerCalled {
		t.Error("expected TracingHook.OnProviderCallStart to be called")
	}
	if !tracingHook.toolCalled {
		t.Error("expected TracingHook.OnToolStart to be called")
	}

	// Verify metrics hook also recorded data by checking the registry.
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
			t.Errorf("metric %q not found — metrics hook may not have fired", name)
			continue
		}
		var total float64
		for _, m := range f.GetMetric() {
			if c := m.GetCounter(); c != nil {
				total += c.GetValue()
			}
		}
		if total <= 0 {
			t.Errorf("metric %q has value %v — expected > 0 (metrics hook should have incremented it)", name, total)
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
			t.Errorf("histogram %q not found — metrics hook may not have fired", name)
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

// ---------------------------------------------------------------------------
// Swarm Metrics Unit Tests
// ---------------------------------------------------------------------------

// TestSwarmMetrics_RunCounterAndDuration verifies swarm run counter and duration
// are recorded on a successful swarm run.
func TestSwarmMetrics_RunCounterAndDuration(t *testing.T) {
	reg := prom.NewRegistry()

	h := &swarmPrometheusHook{
		registerer: reg,
	}
	h.register()

	finish := h.OnSwarmRunStart()
	finish(nil, agent.SwarmResult{})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	familyMap := make(map[string]*dto.MetricFamily)
	for _, f := range families {
		familyMap[f.GetName()] = f
	}

	// Verify swarm_run_total{status="success"} == 1.
	f, ok := familyMap["swarm_run_total"]
	if !ok {
		t.Fatal("swarm_run_total not found in registry")
	}
	found := false
	for _, m := range f.GetMetric() {
		for _, lp := range m.GetLabel() {
			if lp.GetName() == "status" && lp.GetValue() == "success" {
				if v := getCounterValue(m); v != 1 {
					t.Errorf("swarm_run_total{status=success}: expected 1, got %v", v)
				}
				found = true
			}
		}
	}
	if !found {
		t.Error("swarm_run_total{status=success} label not found")
	}

	// Verify swarm_run_duration_seconds has 1 observation.
	f, ok = familyMap["swarm_run_duration_seconds"]
	if !ok {
		t.Fatal("swarm_run_duration_seconds not found in registry")
	}
	for _, m := range f.GetMetric() {
		hist := m.GetHistogram()
		if hist == nil {
			t.Fatal("expected histogram for swarm_run_duration_seconds")
		}
		if hist.GetSampleCount() != 1 {
			t.Errorf("swarm_run_duration_seconds: expected 1 observation, got %d", hist.GetSampleCount())
		}
	}
}

// TestSwarmMetrics_AgentTurnCounter verifies swarm agent turn counter is recorded.
func TestSwarmMetrics_AgentTurnCounter(t *testing.T) {
	reg := prom.NewRegistry()

	h := &swarmPrometheusHook{
		registerer: reg,
	}
	h.register()

	finish := h.OnSwarmAgentStart("billing")
	finish(nil)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	familyMap := make(map[string]*dto.MetricFamily)
	for _, f := range families {
		familyMap[f.GetName()] = f
	}

	f, ok := familyMap["swarm_agent_turn_total"]
	if !ok {
		t.Fatal("swarm_agent_turn_total not found in registry")
	}

	found := false
	for _, m := range f.GetMetric() {
		labels := make(map[string]string)
		for _, lp := range m.GetLabel() {
			labels[lp.GetName()] = lp.GetValue()
		}
		if labels["agent_name"] == "billing" && labels["status"] == "success" {
			if v := getCounterValue(m); v != 1 {
				t.Errorf("swarm_agent_turn_total{agent_name=billing,status=success}: expected 1, got %v", v)
			}
			found = true
		}
	}
	if !found {
		t.Error("swarm_agent_turn_total{agent_name=billing,status=success} not found")
	}
}

// TestSwarmMetrics_HandoffCounter verifies swarm handoff counter is recorded.
func TestSwarmMetrics_HandoffCounter(t *testing.T) {
	reg := prom.NewRegistry()

	h := &swarmPrometheusHook{
		registerer: reg,
	}
	h.register()

	h.OnSwarmHandoff("triage", "billing")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	familyMap := make(map[string]*dto.MetricFamily)
	for _, f := range families {
		familyMap[f.GetName()] = f
	}

	f, ok := familyMap["swarm_handoff_total"]
	if !ok {
		t.Fatal("swarm_handoff_total not found in registry")
	}

	found := false
	for _, m := range f.GetMetric() {
		labels := make(map[string]string)
		for _, lp := range m.GetLabel() {
			labels[lp.GetName()] = lp.GetValue()
		}
		if labels["from"] == "triage" && labels["to"] == "billing" {
			if v := getCounterValue(m); v != 1 {
				t.Errorf("swarm_handoff_total{from=triage,to=billing}: expected 1, got %v", v)
			}
			found = true
		}
	}
	if !found {
		t.Error("swarm_handoff_total{from=triage,to=billing} not found")
	}
}

// ---------------------------------------------------------------------------
// Graph Metrics Unit Tests
// ---------------------------------------------------------------------------

// TestGraphMetrics_RunCounterAndDuration verifies graph run counter and duration
// are recorded on a successful graph run.
func TestGraphMetrics_RunCounterAndDuration(t *testing.T) {
	reg := prom.NewRegistry()

	h := &graphPrometheusHook{
		registerer: reg,
	}
	h.register()

	finish := h.OnGraphRunStart()
	finish(nil, 3)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	familyMap := make(map[string]*dto.MetricFamily)
	for _, f := range families {
		familyMap[f.GetName()] = f
	}

	// Verify graph_run_total{status="success"} == 1.
	f, ok := familyMap["graph_run_total"]
	if !ok {
		t.Fatal("graph_run_total not found in registry")
	}
	found := false
	for _, m := range f.GetMetric() {
		for _, lp := range m.GetLabel() {
			if lp.GetName() == "status" && lp.GetValue() == "success" {
				if v := getCounterValue(m); v != 1 {
					t.Errorf("graph_run_total{status=success}: expected 1, got %v", v)
				}
				found = true
			}
		}
	}
	if !found {
		t.Error("graph_run_total{status=success} label not found")
	}

	// Verify graph_run_duration_seconds has 1 observation.
	f, ok = familyMap["graph_run_duration_seconds"]
	if !ok {
		t.Fatal("graph_run_duration_seconds not found in registry")
	}
	for _, m := range f.GetMetric() {
		hist := m.GetHistogram()
		if hist == nil {
			t.Fatal("expected histogram for graph_run_duration_seconds")
		}
		if hist.GetSampleCount() != 1 {
			t.Errorf("graph_run_duration_seconds: expected 1 observation, got %d", hist.GetSampleCount())
		}
	}
}

// TestGraphMetrics_NodeCounter verifies graph node counter is recorded.
func TestGraphMetrics_NodeCounter(t *testing.T) {
	reg := prom.NewRegistry()

	h := &graphPrometheusHook{
		registerer: reg,
	}
	h.register()

	finish := h.OnNodeStart("fetch")
	finish(nil)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	familyMap := make(map[string]*dto.MetricFamily)
	for _, f := range families {
		familyMap[f.GetName()] = f
	}

	f, ok := familyMap["graph_node_total"]
	if !ok {
		t.Fatal("graph_node_total not found in registry")
	}

	found := false
	for _, m := range f.GetMetric() {
		labels := make(map[string]string)
		for _, lp := range m.GetLabel() {
			labels[lp.GetName()] = lp.GetValue()
		}
		if labels["node_name"] == "fetch" && labels["status"] == "success" {
			if v := getCounterValue(m); v != 1 {
				t.Errorf("graph_node_total{node_name=fetch,status=success}: expected 1, got %v", v)
			}
			found = true
		}
	}
	if !found {
		t.Error("graph_node_total{node_name=fetch,status=success} not found")
	}
}
