package otel

import (
	"context"
	"fmt"
	"sync"
	"testing"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// mockProvider is a minimal Provider for creating agents in tests.
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

// findMetric searches collected ResourceMetrics for a metric with the given name.
func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Unit Tests
// ---------------------------------------------------------------------------

// TestWithMetrics_InstallsHook verifies WithMetrics sets MetricsHook on agent.
// Validates: Requirement 2.5
func TestWithMetrics_InstallsHook(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithMetrics(mp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.MetricsHook() == nil {
		t.Fatal("expected MetricsHook to be set after WithMetrics, got nil")
	}
}

// TestWithMetrics_CustomMeterProvider verifies custom MeterProvider receives metrics.
// Validates: Requirement 2.3
func TestWithMetrics_CustomMeterProvider(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithMetrics(mp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Trigger an invocation via the hook directly.
	hook := a.MetricsHook()
	finish := hook.OnInvokeStart()
	finish(nil, agent.TokenUsage{InputTokens: 10, OutputTokens: 5})

	// Collect metrics via ManualReader.
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	m := findMetric(rm, "agent.invoke.total")
	if m == nil {
		t.Fatal("expected agent.invoke.total metric in custom MeterProvider, not found")
	}
}

// TestWithMetrics_NilMeterProvider verifies nil MeterProvider falls back to global.
// Validates: Requirement 2.4
func TestWithMetrics_NilMeterProvider(t *testing.T) {
	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})

	// Should not panic and should install hook using global provider.
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithMetrics(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.MetricsHook() == nil {
		t.Fatal("expected MetricsHook to be set with nil MeterProvider (global fallback), got nil")
	}
}

// TestWithMetrics_WithNamespace verifies namespace option sets meter scope name.
// Validates: Requirements 6.2, 6.3
func TestWithMetrics_WithNamespace(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	_, err := agent.New(prov, prompt.Text("sys"), nil,
		WithMetrics(mp, WithNamespace("myapp")),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// We need to trigger at least one metric recording so scope appears.
	// Use the hook directly — the agent was created with the hook installed.
	// Record an iteration to produce a metric.
	var rm metricdata.ResourceMetrics

	// Force a metric by creating a fresh hook through the agent.
	// Actually, let's just create the hook directly for scope verification.
	h := &otelHook{}
	WithNamespace("myapp")(h)
	meterName := defaultMeterName
	if h.meterName != "" {
		meterName = h.meterName
	}
	h.meter = mp.Meter(meterName)
	if err := h.register(); err != nil {
		t.Fatalf("register error: %v", err)
	}
	h.OnIterationStart()

	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	// Verify the scope name is "myapp".
	found := false
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name == "myapp" {
			found = true
			break
		}
	}
	if !found {
		var scopes []string
		for _, sm := range rm.ScopeMetrics {
			scopes = append(scopes, sm.Scope.Name)
		}
		t.Fatalf("expected scope name 'myapp', got scopes: %v", scopes)
	}
}

// TestHistogramBuckets verifies histogram buckets match LLM latency spec.
// Validates: Requirement 4.4
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
// Validates: Requirements 4.1, 4.2, 4.3
func TestDurationRecording(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	prov := newMockProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(prov, prompt.Text("sys"), nil, WithMetrics(mp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hook := a.MetricsHook()

	// Exercise all duration-recording hooks.
	finishInvoke := hook.OnInvokeStart()
	finishInvoke(nil, agent.TokenUsage{})

	finishProvider := hook.OnProviderCallStart("test-model")
	finishProvider(nil, agent.TokenUsage{InputTokens: 10, OutputTokens: 5})

	finishTool := hook.OnToolStart("my-tool")
	finishTool(nil)

	// Collect metrics.
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	histogramNames := []string{
		"agent.invoke.duration",
		"agent.provider.call.duration",
		"agent.tool.call.duration",
	}

	for _, name := range histogramNames {
		m := findMetric(rm, name)
		if m == nil {
			t.Errorf("histogram %q not found in collected metrics", name)
			continue
		}

		histData, ok := m.Data.(metricdata.Histogram[float64])
		if !ok {
			t.Errorf("metric %q: expected Histogram[float64] data type, got %T", name, m.Data)
			continue
		}

		if len(histData.DataPoints) == 0 {
			t.Errorf("metric %q: expected at least one data point, got 0", name)
			continue
		}

		for _, dp := range histData.DataPoints {
			if dp.Sum < 0 {
				t.Errorf("metric %q: expected non-negative sum, got %v", name, dp.Sum)
			}
			if dp.Count != 1 {
				t.Errorf("metric %q: expected count 1, got %d", name, dp.Count)
			}
		}
	}
}
