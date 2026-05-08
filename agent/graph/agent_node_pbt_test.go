package graph_test

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// Feature: graph-agent-node-integration, Property 1: Metadata round-trip
//
// For any agent with a random provider name, model ID, agent name, and set of tool
// specifications, when registered via AddAgentNode and then queried via Structure(),
// the resulting NodeInfo SHALL contain the agent's provider name, model ID, label
// (agent name or registration name), and all tool names matching the agent's current ToolSpecs().
//
// **Validates: Requirements 1.1, 1.2**
func TestProperty_MetadataRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random metadata.
		providerName := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "providerName")
		modelID := rapid.StringMatching(`[a-z]{2,8}/[a-z0-9\-]{3,12}`).Draw(rt, "modelID")
		agentName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{2,15}`).Draw(rt, "agentName")
		nodeName := rapid.StringMatching(`[a-z][a-z0-9_]{2,10}`).Draw(rt, "nodeName")

		// Generate 0-10 tool names.
		numTools := rapid.IntRange(0, 10).Draw(rt, "numTools")
		toolNames := make([]string, numTools)
		tools := make([]tool.Tool, numTools)
		for i := 0; i < numTools; i++ {
			toolNames[i] = rapid.StringMatching(`[a-z_]{3,12}`).Draw(rt, "toolName")
			tools[i] = tool.NewSimple(toolNames[i], "desc for "+toolNames[i], func(ctx context.Context) (string, error) {
				return "ok", nil
			})
		}

		// Deduplicate tool names (agent.New rejects duplicates).
		seen := make(map[string]bool)
		var dedupTools []tool.Tool
		var dedupNames []string
		for i, name := range toolNames {
			if !seen[name] {
				seen[name] = true
				dedupTools = append(dedupTools, tools[i])
				dedupNames = append(dedupNames, name)
			}
		}

		// Create provider with model ID.
		sp := &metadataProvider{name: providerName, modelID: modelID}

		// Create agent.
		a, err := agent.New(sp, prompt.Text("test"), dedupTools, agent.WithName(agentName))
		if err != nil {
			rt.Fatalf("agent.New: %v", err)
		}

		// Create graph and register agent node.
		g, err := graph.New[graph.State]()
		if err != nil {
			rt.Fatalf("graph.New: %v", err)
		}

		if _, err := g.Agent(nodeName, a, graph.Keys("output", "input")); err != nil {
			rt.Fatalf("AddAgentNode: %v", err)
		}
		g.Start(nodeName)

		// Query structure.
		structure := g.Structure()
		var nodeInfo *graph.NodeInfo
		for i := range structure.Nodes {
			if structure.Nodes[i].ID == nodeName {
				nodeInfo = &structure.Nodes[i]
				break
			}
		}
		if nodeInfo == nil {
			rt.Fatalf("node %q not found in structure", nodeName)
		}

		// Verify provider name.
		if nodeInfo.Provider != providerName {
			rt.Fatalf("expected provider=%q, got %q", providerName, nodeInfo.Provider)
		}

		// Verify model ID.
		if nodeInfo.Model != modelID {
			rt.Fatalf("expected model=%q, got %q", modelID, nodeInfo.Model)
		}

		// Verify label (agent name when set).
		if nodeInfo.Label != agentName {
			rt.Fatalf("expected label=%q (agent name), got %q", agentName, nodeInfo.Label)
		}

		// Verify tools match current ToolSpecs().
		currentSpecs := a.ToolSpecs()
		if len(nodeInfo.Tools) != len(currentSpecs) {
			rt.Fatalf("expected %d tools, got %d", len(currentSpecs), len(nodeInfo.Tools))
		}
		specNames := make(map[string]bool)
		for _, s := range currentSpecs {
			specNames[s.Name] = true
		}
		for _, tn := range nodeInfo.Tools {
			if !specNames[tn] {
				rt.Fatalf("tool %q in NodeInfo not found in agent ToolSpecs()", tn)
			}
		}
	})
}

// Feature: graph-agent-node-integration, Property 9: Registration validation
//
// For any call to AddAgentNode where the node name is empty OR the node name is
// already registered OR the agent is nil, the function SHALL return a non-nil error
// with a descriptive message.
//
// **Validates: Requirements 6.3, 6.4**
func TestProperty_RegistrationValidation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		scenario := rapid.IntRange(0, 2).Draw(rt, "scenario")

		sp := newScriptedProvider(&agent.ProviderResponse{Text: "hello"})
		a, err := agent.New(sp, prompt.Text("test"), nil)
		if err != nil {
			rt.Fatalf("agent.New: %v", err)
		}

		g, err := graph.New[graph.State]()
		if err != nil {
			rt.Fatalf("graph.New: %v", err)
		}

		switch scenario {
		case 0:
			// Empty node name.
			_, err := g.Agent("", a, graph.Keys("output", "input"))
			if err == nil {
				rt.Fatal("expected error for empty node name, got nil")
			}

		case 1:
			// Duplicate node name.
			nodeName := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "nodeName")
			if _, err := g.Agent(nodeName, a, graph.Keys("output", "input")); err != nil {
				rt.Fatalf("first AddAgentNode: %v", err)
			}
			_, err := g.Agent(nodeName, a, graph.Keys("output", "input"))
			if err == nil {
				rt.Fatalf("expected error for duplicate node name %q, got nil", nodeName)
			}

		case 2:
			// Nil agent.
			nodeName := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "nodeName")
			_, err := g.Agent(nodeName, nil, graph.Keys("output", "input"))
			if err == nil {
				rt.Fatal("expected error for nil agent, got nil")
			}
		}
	})
}

// Feature: graph-agent-node-integration, Property 10: Generic state type parity
//
// For any struct state type S with an AgentNodeAccessor[S], when an agent is registered
// via the generic AddAgentNodeGeneric[S], the resulting node SHALL populate metadata,
// emit events, and inherit hooks identically to the Graph[State] version.
//
// **Validates: Requirements 6.5**
func TestProperty_GenericStateParity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		providerName := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "providerName")
		modelID := rapid.StringMatching(`[a-z]{2,8}/[a-z0-9\-]{3,12}`).Draw(rt, "modelID")
		agentName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{2,15}`).Draw(rt, "agentName")
		nodeName := rapid.StringMatching(`[a-z][a-z0-9_]{2,10}`).Draw(rt, "nodeName")
		inputMsg := rapid.StringMatching(`[a-zA-Z ]{1,50}`).Draw(rt, "inputMsg")

		// Generate 0-5 tool names.
		numTools := rapid.IntRange(0, 5).Draw(rt, "numTools")
		var tools []tool.Tool
		seen := make(map[string]bool)
		for i := 0; i < numTools; i++ {
			tn := rapid.StringMatching(`[a-z_]{3,12}`).Draw(rt, "toolName")
			if seen[tn] {
				continue
			}
			seen[tn] = true
			tools = append(tools, tool.NewSimple(tn, "desc", func(ctx context.Context) (string, error) {
				return "ok", nil
			}))
		}

		// Create provider with model ID.
		sp := &metadataProvider{name: providerName, modelID: modelID}

		// --- Graph[State] version ---
		a1, err := agent.New(sp, prompt.Text("test"), tools, agent.WithName(agentName))
		if err != nil {
			rt.Fatalf("agent.New (map): %v", err)
		}

		eventHook1 := &collectingEventHook{}
		g1, err := graph.New[graph.State](graph.WithEventHook(eventHook1))
		if err != nil {
			rt.Fatalf("graph.New[State]: %v", err)
		}

		if _, err := g1.Agent(nodeName, a1, graph.Keys("output", "input")); err != nil {
			rt.Fatalf("AddAgentNode: %v", err)
		}
		g1.Start(nodeName)

		// --- Generic Graph[S] version ---
		type MyState struct {
			Input  string
			Output string
		}

		a2, err := agent.New(sp, prompt.Text("test"), tools, agent.WithName(agentName))
		if err != nil {
			rt.Fatalf("agent.New (generic): %v", err)
		}

		eventHook2 := &collectingEventHook{}
		g2, err := graph.New[MyState](graph.WithEventHook(eventHook2))
		if err != nil {
			rt.Fatalf("graph.New[MyState]: %v", err)
		}

		accessor := graph.AgentNodeAccessor[MyState]{
			GetInput:   func(s MyState) string { return s.Input },
			SetOutput:  func(s *MyState, out string) { s.Output = out },
			InputKeys:  []string{"input"},
			OutputKeys: []string{"output"},
		}

		if _, err := g2.Agent(nodeName, a2, accessor); err != nil {
			rt.Fatalf("AddAgentNodeGeneric: %v", err)
		}
		g2.Start(nodeName)

		// Compare metadata via Structure().
		s1 := g1.Structure()
		s2 := g2.Structure()

		var ni1, ni2 *graph.NodeInfo
		for i := range s1.Nodes {
			if s1.Nodes[i].ID == nodeName {
				ni1 = &s1.Nodes[i]
				break
			}
		}
		for i := range s2.Nodes {
			if s2.Nodes[i].ID == nodeName {
				ni2 = &s2.Nodes[i]
				break
			}
		}

		if ni1 == nil || ni2 == nil {
			rt.Fatal("node not found in one of the structures")
		}

		// Verify metadata parity.
		if ni1.Provider != ni2.Provider {
			rt.Fatalf("provider mismatch: %q vs %q", ni1.Provider, ni2.Provider)
		}
		if ni1.Model != ni2.Model {
			rt.Fatalf("model mismatch: %q vs %q", ni1.Model, ni2.Model)
		}
		if ni1.Label != ni2.Label {
			rt.Fatalf("label mismatch: %q vs %q", ni1.Label, ni2.Label)
		}
		if len(ni1.Tools) != len(ni2.Tools) {
			rt.Fatalf("tools count mismatch: %d vs %d", len(ni1.Tools), len(ni2.Tools))
		}

		// Run both graphs and compare event emission.
		_, err1 := g1.Run(context.Background(), graph.State{"input": inputMsg})
		_, err2 := g2.Run(context.Background(), MyState{Input: inputMsg})

		// Both should succeed or both should fail.
		if (err1 == nil) != (err2 == nil) {
			rt.Fatalf("execution parity mismatch: map err=%v, generic err=%v", err1, err2)
		}

		if err1 == nil {
			// Verify event parity: same number and types of events.
			if len(eventHook1.events) != len(eventHook2.events) {
				rt.Fatalf("event count mismatch: %d vs %d", len(eventHook1.events), len(eventHook2.events))
			}
			for i := range eventHook1.events {
				if eventHook1.events[i].Type != eventHook2.events[i].Type {
					rt.Fatalf("event[%d] type mismatch: %q vs %q", i, eventHook1.events[i].Type, eventHook2.events[i].Type)
				}
				if eventHook1.events[i].NodeName != eventHook2.events[i].NodeName {
					rt.Fatalf("event[%d] node name mismatch: %q vs %q", i, eventHook1.events[i].NodeName, eventHook2.events[i].NodeName)
				}
			}
		}
	})
}

// ── Test helpers ─────────────────────────────────────────────────────────────

// metadataProvider is a mock provider that exposes a configurable name and model ID.
type metadataProvider struct {
	name    string
	modelID string
}

func (p *metadataProvider) Name() string    { return p.name }
func (p *metadataProvider) ModelID() string { return p.modelID }

func (p *metadataProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	return &agent.ProviderResponse{Text: "response"}, nil
}

func (p *metadataProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	if cb != nil {
		cb("response")
	}
	return &agent.ProviderResponse{Text: "response"}, nil
}

// collectingEventHook collects all events for verification.
type collectingEventHook struct {
	events []graph.GraphEvent
}

func (h *collectingEventHook) OnEvent(event graph.GraphEvent) {
	h.events = append(h.events, event)
}
