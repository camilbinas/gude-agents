package graph_test

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestAgentNode_TracingHookInheritance validates Req 3.1:
// WHEN a Graph has a GraphTracingHook configured and an Agent_Node executes,
// THE Agent_Node SHALL use a TracingHook that creates child spans under the graph's node span.
func TestAgentNode_TracingHookInheritance(t *testing.T) {
	sp := newScriptedProvider(&agent.ProviderResponse{Text: "traced response"})
	a, err := agent.New(sp, prompt.Text("test agent"), nil)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	tracingHook := &integrationTracingHook{}
	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	g.SetGraphTracingHook(tracingHook)

	if _, err := g.Agent("traced_agent", a, graph.Keys("output", "input")); err != nil {
		t.Fatalf("AddAgentNode: %v", err)
	}
	g.Start("traced_agent")

	_, err = g.Run(context.Background(), graph.State{"input": "hello"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify the tracing hook received OnNodeStart calls.
	if tracingHook.nodeStartCalls == 0 {
		t.Error("expected at least one OnNodeStart call from tracing hook, got 0")
	}

	// Verify the graph run start was called.
	if tracingHook.graphRunStartCalls == 0 {
		t.Error("expected OnGraphRunStart to be called")
	}
}

// TestAgentNode_MetricsHookInheritance validates Req 3.2:
// WHEN a Graph has a GraphMetricsHook configured and an Agent_Node executes,
// THE Agent_Node SHALL report metrics through a MetricsHook derived from the graph's metrics context.
func TestAgentNode_MetricsHookInheritance(t *testing.T) {
	sp := newScriptedProvider(&agent.ProviderResponse{Text: "metrics response"})
	a, err := agent.New(sp, prompt.Text("test agent"), nil)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	metricsHook := &integrationMetricsHook{}
	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	g.SetGraphMetricsHook(metricsHook)

	if _, err := g.Agent("metrics_agent", a, graph.Keys("output", "input")); err != nil {
		t.Fatalf("AddAgentNode: %v", err)
	}
	g.Start("metrics_agent")

	_, err = g.Run(context.Background(), graph.State{"input": "hello"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify the metrics hook received OnNodeStart calls.
	if metricsHook.nodeStartCalls == 0 {
		t.Error("expected at least one OnNodeStart call from metrics hook, got 0")
	}

	// Verify the graph run start was called.
	if metricsHook.graphRunStartCalls == 0 {
		t.Error("expected OnGraphRunStart to be called")
	}
}

// TestAgentNode_FullPipeline is an end-to-end integration test that verifies:
// - Metadata is populated in Structure()
// - Events are emitted (at minimum: model start, streaming, model end)
// - Output state contains the response
func TestAgentNode_FullPipeline(t *testing.T) {
	sp := newScriptedProvider(&agent.ProviderResponse{
		Text:  "full pipeline response",
		Usage: agent.TokenUsage{InputTokens: 15, OutputTokens: 8},
	})
	a, err := agent.New(sp, prompt.Text("pipeline agent"), nil, agent.WithName("pipeline"))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	eventHook := &collectingEventHook{}
	g, err := graph.New[graph.State](graph.WithEventHook(eventHook))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}

	if _, err := g.Agent("pipeline_node", a, graph.Keys("output", "input")); err != nil {
		t.Fatalf("AddAgentNode: %v", err)
	}
	g.Start("pipeline_node")

	// Verify metadata is populated in Structure().
	structure := g.Structure()
	var nodeInfo *graph.NodeInfo
	for i := range structure.Nodes {
		if structure.Nodes[i].ID == "pipeline_node" {
			nodeInfo = &structure.Nodes[i]
			break
		}
	}
	if nodeInfo == nil {
		t.Fatal("expected to find node 'pipeline_node' in structure")
	}
	if nodeInfo.Provider != "mock" {
		t.Errorf("expected provider='mock', got %q", nodeInfo.Provider)
	}
	if nodeInfo.Label != "pipeline" {
		t.Errorf("expected label='pipeline' (agent name), got %q", nodeInfo.Label)
	}

	// Run the graph.
	res, err := g.Run(context.Background(), graph.State{"input": "test input"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify output state contains the response.
	output, ok := res.State["output"].(string)
	if !ok {
		t.Fatalf("expected output to be a string, got %T", res.State["output"])
	}
	if output == "" {
		t.Error("expected non-empty output")
	}

	// Verify events were emitted. We expect at minimum:
	// GraphStarted, NodeStarted, [agent events: model start, streaming, model end], NodeCompleted, GraphCompleted
	if len(eventHook.events) < 3 {
		t.Fatalf("expected at least 3 events, got %d", len(eventHook.events))
	}

	// Check for agent-level events (model start, streaming, model end).
	hasModelStart := false
	hasStreaming := false
	hasModelEnd := false
	for _, ev := range eventHook.events {
		switch ev.Type {
		case graph.EventAgentModelStart:
			hasModelStart = true
		case graph.EventAgentStreaming:
			hasStreaming = true
		case graph.EventAgentModelEnd:
			hasModelEnd = true
		}
	}

	if !hasModelStart {
		t.Error("expected AgentModelStart event to be emitted")
	}
	if !hasStreaming {
		t.Error("expected AgentStreaming event to be emitted")
	}
	if !hasModelEnd {
		t.Error("expected AgentModelEnd event to be emitted")
	}
}

// TestAddAgentNode_SingleOperation validates Req 6.2:
// WHEN AddAgentNode is called, THE Graph SHALL register the node function, populate
// metadata, and configure event forwarding and hook inheritance in a single operation.
func TestAddAgentNode_SingleOperation(t *testing.T) {
	// Create an agent with tools.
	myTool := tool.NewSimple("search", "search the web", func(ctx context.Context) (string, error) {
		return "results", nil
	})

	sp := &metadataProvider{name: "openai", modelID: "gpt-4"}
	a, err := agent.New(sp, prompt.Text("test agent"), []tool.Tool{myTool}, agent.WithName("my_agent"))
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}

	// Call AddAgentNode once.
	if _, err := g.Agent("agent_node", a, graph.Keys("output", "input")); err != nil {
		t.Fatalf("AddAgentNode: %v", err)
	}
	g.Start("agent_node")

	// Verify the node is registered (can be found in Structure()).
	structure := g.Structure()
	var nodeInfo *graph.NodeInfo
	for i := range structure.Nodes {
		if structure.Nodes[i].ID == "agent_node" {
			nodeInfo = &structure.Nodes[i]
			break
		}
	}
	if nodeInfo == nil {
		t.Fatal("expected to find node 'agent_node' in structure")
	}

	// Verify metadata is populated (provider, model, label).
	if nodeInfo.Provider != "openai" {
		t.Errorf("expected provider='openai', got %q", nodeInfo.Provider)
	}
	if nodeInfo.Model != "gpt-4" {
		t.Errorf("expected model='gpt-4', got %q", nodeInfo.Model)
	}
	if nodeInfo.Label != "my_agent" {
		t.Errorf("expected label='my_agent', got %q", nodeInfo.Label)
	}

	// Verify the agent is in the registry (tools show up in Structure()).
	if len(nodeInfo.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d: %v", len(nodeInfo.Tools), nodeInfo.Tools)
	}
	if nodeInfo.Tools[0] != "search" {
		t.Errorf("expected tool='search', got %q", nodeInfo.Tools[0])
	}
}

// ── Integration test helpers ─────────────────────────────────────────────────

// integrationTracingHook records tracing calls for integration testing.
type integrationTracingHook struct {
	graphRunStartCalls int
	nodeStartCalls     int
	nodeEndCalls       int
}

func (h *integrationTracingHook) OnGraphRunStart(ctx context.Context) (context.Context, func(error, int)) {
	h.graphRunStartCalls++
	return ctx, func(_ error, _ int) {}
}

func (h *integrationTracingHook) OnNodeStart(ctx context.Context, _ string) (context.Context, func(error)) {
	h.nodeStartCalls++
	return ctx, func(_ error) {
		h.nodeEndCalls++
	}
}

func (h *integrationTracingHook) OnCheckpointSave(_ context.Context, _ string, _ int) func(error) {
	return func(_ error) {}
}

func (h *integrationTracingHook) OnInterrupt(_ context.Context, _ string, _ graph.InterruptType, _ int) {
}
func (h *integrationTracingHook) OnResume(_ context.Context, _ string, _ int) {}
func (h *integrationTracingHook) OnRewind(_ context.Context, _ string, _ int) {}

// integrationMetricsHook records metrics calls for integration testing.
type integrationMetricsHook struct {
	graphRunStartCalls int
	nodeStartCalls     int
	nodeEndCalls       int
}

func (h *integrationMetricsHook) OnGraphRunStart() func(error, int) {
	h.graphRunStartCalls++
	return func(_ error, _ int) {}
}

func (h *integrationMetricsHook) OnNodeStart(_ string) func(error) {
	h.nodeStartCalls++
	return func(_ error) {
		h.nodeEndCalls++
	}
}
