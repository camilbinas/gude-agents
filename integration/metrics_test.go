package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/metrics/prometheus"
	"github.com/camilbinas/gude-agents/agent/prompt"

	prom "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// getCounterValue extracts the counter value from a prometheus Metric.
func getCounterValue(m *dto.Metric) float64 {
	if c := m.GetCounter(); c != nil {
		return c.GetValue()
	}
	return 0
}

// sumCounter sums all counter values across label combinations for a metric family.
func sumCounter(familyMap map[string]*dto.MetricFamily, name string, t *testing.T) float64 {
	t.Helper()
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

// sumHistogramCount sums all histogram sample counts across label combinations.
func sumHistogramCount(familyMap map[string]*dto.MetricFamily, name string, t *testing.T) uint64 {
	t.Helper()
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

// gatherFamilyMap gathers metrics from a registry and returns a lookup map.
func gatherFamilyMap(reg *prom.Registry, t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	m := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		m[f.GetName()] = f
	}
	return m
}

// ---------------------------------------------------------------------------
// Integration Tests
// ---------------------------------------------------------------------------

// TestIntegration_Metrics_AgentInvocation verifies that Prometheus agent-level
// metrics are recorded during a real LLM invocation.
func TestIntegration_Metrics_AgentInvocation(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	reg := prom.NewRegistry()
	a, err := agent.New(p, prompt.Text("You are a helpful assistant. Be brief."), nil,
		prometheus.WithMetrics(prometheus.WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "What is 2+2? Answer with just the number.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	t.Logf("Response: %s", result)

	familyMap := gatherFamilyMap(reg, t)

	// Verify agent_invoke_total >= 1.
	if v := sumCounter(familyMap, "agent_invoke_total", t); v < 1 {
		t.Errorf("agent_invoke_total: expected >= 1, got %v", v)
	}

	// Verify agent_iteration_total >= 1.
	if v := sumCounter(familyMap, "agent_iteration_total", t); v < 1 {
		t.Errorf("agent_iteration_total: expected >= 1, got %v", v)
	}

	// Verify agent_provider_call_total >= 1.
	if v := sumCounter(familyMap, "agent_provider_call_total", t); v < 1 {
		t.Errorf("agent_provider_call_total: expected >= 1, got %v", v)
	}

	// Verify agent_invoke_duration_seconds has observations.
	if v := sumHistogramCount(familyMap, "agent_invoke_duration_seconds", t); v < 1 {
		t.Errorf("agent_invoke_duration_seconds: expected >= 1 observation, got %d", v)
	}
}
