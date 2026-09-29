package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/metrics/prometheus"

	prom "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func getCounterValue(m *dto.Metric) float64 {
	if counter := m.GetCounter(); counter != nil {
		return counter.GetValue()
	}
	return 0
}

func sumCounter(familyMap map[string]*dto.MetricFamily, name string, t *testing.T) float64 {
	t.Helper()
	family, ok := familyMap[name]
	if !ok {
		t.Errorf("metric %q not found in registry", name)
		return 0
	}
	var total float64
	for _, metric := range family.GetMetric() {
		if counter := metric.GetCounter(); counter != nil {
			total += counter.GetValue()
		}
	}
	return total
}

func sumHistogramCount(familyMap map[string]*dto.MetricFamily, name string, t *testing.T) uint64 {
	t.Helper()
	family, ok := familyMap[name]
	if !ok {
		t.Errorf("metric %q not found in registry", name)
		return 0
	}
	var total uint64
	for _, metric := range family.GetMetric() {
		if histogram := metric.GetHistogram(); histogram != nil {
			total += histogram.GetSampleCount()
		}
	}
	return total
}

func gatherFamilyMap(registry *prom.Registry, t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	result := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		result[family.GetName()] = family
	}
	return result
}

func TestIntegration_Metrics_AgentInvocation(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	registry := prom.NewRegistry()
	a, err := agent.New(
		p,
		"You are a helpful assistant. Be brief.",
		prometheus.WithMetrics(prometheus.WithRegisterer(registry)),
	)
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := a.Invoke(agent.NewContext(ctx), "What is 2+2? Answer with just the number.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	t.Logf("Response: %s", result.Text)

	familyMap := gatherFamilyMap(registry, t)
	if value := sumCounter(familyMap, "agent_invoke_total", t); value < 1 {
		t.Errorf("agent_invoke_total: expected >= 1, got %v", value)
	}
	if value := sumCounter(familyMap, "agent_iteration_total", t); value < 1 {
		t.Errorf("agent_iteration_total: expected >= 1, got %v", value)
	}
	if value := sumCounter(familyMap, "agent_provider_call_total", t); value < 1 {
		t.Errorf("agent_provider_call_total: expected >= 1, got %v", value)
	}
	if value := sumHistogramCount(familyMap, "agent_invoke_duration_seconds", t); value < 1 {
		t.Errorf("agent_invoke_duration_seconds: expected >= 1 observation, got %d", value)
	}
}
