package graph_test

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestAddAgentNode_DynamicToolUpdate verifies Req 1.3:
// WHEN an agent registered as an Agent_Node has its tools modified after registration,
// THE Graph SHALL reflect the current tool specifications at the time Structure() is called.
func TestAddAgentNode_DynamicToolUpdate(t *testing.T) {
	// Create an agent with one initial tool.
	initialTool := tool.NewSimple("search", "search the web", func(ctx context.Context) (string, error) {
		return "results", nil
	})

	sp := newScriptedProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(sp, prompt.Text("test agent"), []tool.Tool{initialTool})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}

	if _, err := g.Agent("myagent", a, graph.Keys("output", "input")); err != nil {
		t.Fatalf("AddAgentNode: %v", err)
	}
	g.Start("myagent")

	// First call to Structure() should show the initial tool.
	structure := g.Structure()
	var nodeInfo *graph.NodeInfo
	for i := range structure.Nodes {
		if structure.Nodes[i].ID == "myagent" {
			nodeInfo = &structure.Nodes[i]
			break
		}
	}
	if nodeInfo == nil {
		t.Fatal("expected to find node 'myagent' in structure")
	}
	if len(nodeInfo.Tools) != 1 || nodeInfo.Tools[0] != "search" {
		t.Fatalf("expected tools=[search], got %v", nodeInfo.Tools)
	}

	// Now register a new tool on the agent after registration.
	newTool := tool.NewSimple("calculator", "do math", func(ctx context.Context) (string, error) {
		return "42", nil
	})
	if err := a.RegisterTool(newTool); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	// Second call to Structure() should reflect the updated tools.
	structure2 := g.Structure()
	var nodeInfo2 *graph.NodeInfo
	for i := range structure2.Nodes {
		if structure2.Nodes[i].ID == "myagent" {
			nodeInfo2 = &structure2.Nodes[i]
			break
		}
	}
	if nodeInfo2 == nil {
		t.Fatal("expected to find node 'myagent' in structure after tool update")
	}
	if len(nodeInfo2.Tools) != 2 {
		t.Fatalf("expected 2 tools after update, got %d: %v", len(nodeInfo2.Tools), nodeInfo2.Tools)
	}

	// Verify both tools are present.
	toolSet := make(map[string]bool)
	for _, name := range nodeInfo2.Tools {
		toolSet[name] = true
	}
	if !toolSet["search"] {
		t.Error("expected 'search' tool to be present after update")
	}
	if !toolSet["calculator"] {
		t.Error("expected 'calculator' tool to be present after update")
	}
}

// TestAddAgentNode_NoAgentName_UsesNodeName verifies Req 1.4:
// IF an agent has no name configured, THEN THE Graph SHALL use the node registration name
// as the label in Node_Meta.
func TestAddAgentNode_NoAgentName_UsesNodeName(t *testing.T) {
	// Create an agent WITHOUT a name configured.
	sp := newScriptedProvider(&agent.ProviderResponse{Text: "hello"})
	a, err := agent.New(sp, prompt.Text("test agent"), nil)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	// Verify the agent has no name.
	if a.Name() != "" {
		t.Fatalf("expected agent to have no name, got %q", a.Name())
	}

	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}

	registrationName := "my_custom_node"
	if _, err := g.Agent(registrationName, a, graph.Keys("output", "input")); err != nil {
		t.Fatalf("AddAgentNode: %v", err)
	}
	g.Start(registrationName)

	// Structure() should use the registration name as the label.
	structure := g.Structure()
	var nodeInfo *graph.NodeInfo
	for i := range structure.Nodes {
		if structure.Nodes[i].ID == registrationName {
			nodeInfo = &structure.Nodes[i]
			break
		}
	}
	if nodeInfo == nil {
		t.Fatalf("expected to find node %q in structure", registrationName)
	}
	if nodeInfo.Label != registrationName {
		t.Errorf("expected label=%q (registration name), got %q", registrationName, nodeInfo.Label)
	}
}
