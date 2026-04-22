package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/metrics/prometheus"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/swarm"
	"github.com/camilbinas/gude-agents/agent/tool"

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

	result, _, err := a.Invoke(ctx, "What is 2+2? Answer with just the number.")
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

// TestIntegration_Metrics_SwarmWithHandoff verifies that Prometheus swarm metrics
// are recorded during a real swarm invocation with handoff.
func TestIntegration_Metrics_SwarmWithHandoff(t *testing.T) {
	p := newTestProvider(t)

	reg := prom.NewRegistry()

	triage, err := agent.SwarmAgent(p, prompt.Text(
		"You are a triage agent. You CANNOT answer billing questions yourself. "+
			"If the user asks about refunds, invoices, or payments, you MUST transfer to billing immediately. "+
			"Do not attempt to answer — just transfer.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	type RefundInput struct {
		OrderID string `json:"order_id" description:"The order ID to refund" required:"true"`
	}
	refundTool := tool.New("process_refund", "Process a refund for an order",
		func(_ context.Context, in RefundInput) (string, error) {
			return `{"status":"refunded","order":"` + in.OrderID + `","amount":"$49.99"}`, nil
		},
	)

	billing, err := agent.SwarmAgent(p, prompt.Text(
		"You are a billing specialist. Help users with refunds and payments. "+
			"Use the process_refund tool when asked. Be brief.",
	), []tool.Tool{refundTool})
	if err != nil {
		t.Fatal(err)
	}

	sw, err := swarm.New([]swarm.Member{
		{Name: "triage", Description: "Routes requests to the right specialist", Agent: triage},
		{Name: "billing", Description: "Handles refunds, invoices, and payments", Agent: billing},
	},
		swarm.WithMaxHandoffs(3),
		
		prometheus.WithSwarmMetrics(prometheus.WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := sw.Invoke(ctx, "I need a refund for order #1234")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	t.Logf("Final agent: %s", result.FinalAgent)
	t.Logf("Handoffs: %v", result.HandoffHistory)
	t.Logf("Response: %s", result.Response)

	familyMap := gatherFamilyMap(reg, t)

	// Verify swarm_run_total{status="success"} >= 1.
	if v := sumCounter(familyMap, "swarm_run_total", t); v < 1 {
		t.Errorf("swarm_run_total: expected >= 1, got %v", v)
	}

	// Verify swarm_handoff_total >= 1 (triage should hand off to billing).
	if v := sumCounter(familyMap, "swarm_handoff_total", t); v < 1 {
		t.Errorf("swarm_handoff_total: expected >= 1, got %v", v)
	}

	// Verify swarm_agent_turn_total >= 2 (at least triage + billing turns).
	if v := sumCounter(familyMap, "swarm_agent_turn_total", t); v < 2 {
		t.Errorf("swarm_agent_turn_total: expected >= 2, got %v", v)
	}
}

// TestIntegration_Metrics_GraphPipeline verifies that Prometheus graph metrics
// are recorded during a simple graph execution.
func TestIntegration_Metrics_GraphPipeline(t *testing.T) {
	reg := prom.NewRegistry()

	g, err := graph.NewGraph(
		prometheus.WithGraphMetrics(prometheus.WithRegisterer(reg)),
	)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}

	// step1: sets a value in state.
	err = g.AddNode("step1", func(_ context.Context, state graph.State) (graph.State, error) {
		state["value"] = 1
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// step2: increments the value.
	err = g.AddNode("step2", func(_ context.Context, state graph.State) (graph.State, error) {
		v, _ := state["value"].(int)
		state["value"] = v + 1
		return state, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	g.SetEntry("step1")
	if err := g.AddEdge("step1", "step2"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := g.Run(ctx, graph.State{})
	if err != nil {
		t.Fatalf("graph.Run error: %v", err)
	}

	if v, ok := result.State["value"].(int); !ok || v != 2 {
		t.Errorf("expected state[value]=2, got %v", result.State["value"])
	}

	familyMap := gatherFamilyMap(reg, t)

	// Verify graph_run_total{status="success"} == 1.
	if v := sumCounter(familyMap, "graph_run_total", t); v != 1 {
		t.Errorf("graph_run_total: expected 1, got %v", v)
	}

	// Verify graph_node_total >= 2 (step1 + step2).
	if v := sumCounter(familyMap, "graph_node_total", t); v < 2 {
		t.Errorf("graph_node_total: expected >= 2, got %v", v)
	}
}
