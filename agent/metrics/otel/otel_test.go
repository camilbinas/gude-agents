package otel

import (
	"context"
	"testing"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

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

// TestWithMetrics_RegistersObserver verifies WithMetrics installs a working observer.
func TestWithMetrics_RegistersObserver(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "hello"}))
	a, err := agent.New(prov, "sys", WithMetrics(mp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := a.Invoke(agent.Background(), "hello"); err != nil {
		t.Fatalf("invoke error: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}
	if findMetric(rm, "agent.invoke.total") == nil {
		t.Fatal("expected observer to record agent.invoke.total")
	}
}

// TestWithMetrics_CustomMeterProvider verifies custom MeterProvider receives metrics.
func TestWithMetrics_CustomMeterProvider(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "hello"}))
	a, err := agent.New(prov, "sys", agent.WithName("metrics-agent"), WithMetrics(mp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Trigger an invocation through the registered observer.
	if _, err := a.Invoke(agent.Background(), "hello"); err != nil {
		t.Fatalf("invoke error: %v", err)
	}

	// Collect metrics via ManualReader.
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	m := findMetric(rm, "agent.invoke.total")
	if m == nil {
		t.Fatal("expected agent.invoke.total metric in custom MeterProvider, not found")
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("agent.invoke.total data type = %T, want metricdata.Sum[int64]", m.Data)
	}
	foundAgentName := false
	for _, point := range sum.DataPoints {
		if matchOTELAttrs(point, map[string]string{"agent_name": "metrics-agent", "status": "success"}) {
			foundAgentName = true
		}
	}
	if !foundAgentName {
		t.Fatal("expected agent_name attribute on invocation metric")
	}
}

// TestWithMetrics_NilMeterProvider verifies nil MeterProvider falls back to global.
func TestWithMetrics_NilMeterProvider(t *testing.T) {
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "hello"}))

	// Should not panic and should install hook using global provider.
	a, err := agent.New(prov, "sys", WithMetrics(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a == nil {
		t.Fatal("expected agent construction with global MeterProvider to succeed")
	}
}

// TestWithMetrics_WithNamespace verifies namespace option sets meter scope name.
func TestWithMetrics_WithNamespace(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "hello"}))
	_, err := agent.New(prov, "sys",
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
	_ = h.ObserveIteration(context.Background(), agent.IterationRecord{Phase: agent.Start})

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
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ModelResponse{Text: "hello"}))
	a, err := agent.New(prov, "sys", WithMetrics(mp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := a.Invoke(agent.Background(), "hello"); err != nil {
		t.Fatalf("invoke error: %v", err)
	}

	// Register direct normalized records for provider and tool duration coverage.
	h := &otelHook{meter: mp.Meter("duration-direct")}
	if err := h.register(); err != nil {
		t.Fatalf("register error: %v", err)
	}
	_ = h.ObserveModel(context.Background(), agent.ModelCallRecord{
		Phase: agent.End, ModelID: "test-model", Duration: 3,
		Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
	})
	_ = h.ObserveTool(context.Background(), agent.ToolCallRecord{
		Phase: agent.End, Name: "my-tool", Duration: 4,
	})
	_ = h.ObserveAttachment(context.Background(), agent.AttachmentRecord{
		Phase: agent.End, ImageCount: 2, DocumentCount: 3,
	})

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

	for _, name := range []string{"agent.images.attached.total", "agent.documents.attached.total"} {
		if findMetric(rm, name) == nil {
			t.Errorf("attachment metric %q not found", name)
		}
	}
}
