package graph

import (
	"context"
	"testing"
)

// TestStructure_LinearChain verifies DataFlowEdges for a linear chain: A→B→C.
func TestStructure_LinearChain(t *testing.T) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	// A produces "x", B consumes "x" and produces "y", C consumes "y" and produces "z".
	if _, err := g.Node("A", func(_ context.Context, s State) (State, error) {
		return State{"x": "a_out"}, nil
	}, In(), Out("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("B", func(_ context.Context, s State) (State, error) {
		return State{"y": "b_out"}, nil
	}, In("x"), Out("y")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("C", func(_ context.Context, s State) (State, error) {
		return State{"z": "c_out"}, nil
	}, In("y"), Out("z")); err != nil {
		t.Fatal(err)
	}
	g.Start("A")

	structure := g.Structure()

	// Expect 2 edges: A→B (key "x"), B→C (key "y").
	if len(structure.DataFlowEdges) != 2 {
		t.Fatalf("expected 2 DataFlowEdges, got %d: %+v", len(structure.DataFlowEdges), structure.DataFlowEdges)
	}

	edgeSet := make(map[string]bool)
	for _, e := range structure.DataFlowEdges {
		edgeSet[e.From+"|"+e.To+"|"+e.Key] = true
	}

	if !edgeSet["A|B|x"] {
		t.Errorf("missing edge A→B on key 'x'")
	}
	if !edgeSet["B|C|y"] {
		t.Errorf("missing edge B→C on key 'y'")
	}
}

// TestStructure_DiamondTopology verifies DataFlowEdges for a diamond: A→(B,C)→D.
func TestStructure_DiamondTopology(t *testing.T) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	// A produces "x", B consumes "x" produces "b_out", C consumes "x" produces "c_out",
	// D consumes "b_out" and "c_out".
	if _, err := g.Node("A", func(_ context.Context, s State) (State, error) {
		return State{"x": "a"}, nil
	}, In(), Out("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("B", func(_ context.Context, s State) (State, error) {
		return State{"b_out": "b"}, nil
	}, In("x"), Out("b_out")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("C", func(_ context.Context, s State) (State, error) {
		return State{"c_out": "c"}, nil
	}, In("x"), Out("c_out")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("D", func(_ context.Context, s State) (State, error) {
		return State{"result": "d"}, nil
	}, In("b_out", "c_out"), Out("result")); err != nil {
		t.Fatal(err)
	}
	g.Start("A")

	structure := g.Structure()

	// Expect 4 edges: A→B (x), A→C (x), B→D (b_out), C→D (c_out).
	if len(structure.DataFlowEdges) != 4 {
		t.Fatalf("expected 4 DataFlowEdges, got %d: %+v", len(structure.DataFlowEdges), structure.DataFlowEdges)
	}

	edgeSet := make(map[string]bool)
	for _, e := range structure.DataFlowEdges {
		edgeSet[e.From+"|"+e.To+"|"+e.Key] = true
	}

	if !edgeSet["A|B|x"] {
		t.Errorf("missing edge A→B on key 'x'")
	}
	if !edgeSet["A|C|x"] {
		t.Errorf("missing edge A→C on key 'x'")
	}
	if !edgeSet["B|D|b_out"] {
		t.Errorf("missing edge B→D on key 'b_out'")
	}
	if !edgeSet["C|D|c_out"] {
		t.Errorf("missing edge C→D on key 'c_out'")
	}
}

// TestStructure_MultipleInputKeysFromDifferentProducers verifies that multiple
// input keys from different producers emit separate edges.
func TestStructure_MultipleInputKeysFromDifferentProducers(t *testing.T) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	// A produces "alpha", B produces "beta", C consumes both "alpha" and "beta".
	if _, err := g.Node("A", func(_ context.Context, s State) (State, error) {
		return State{"alpha": "a"}, nil
	}, In(), Out("alpha")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("B", func(_ context.Context, s State) (State, error) {
		return State{"beta": "b"}, nil
	}, In(), Out("beta")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("C", func(_ context.Context, s State) (State, error) {
		return State{"result": "c"}, nil
	}, In("alpha", "beta"), Out("result")); err != nil {
		t.Fatal(err)
	}
	g.Start("A")

	structure := g.Structure()

	// Expect 2 edges: A→C (alpha), B→C (beta).
	if len(structure.DataFlowEdges) != 2 {
		t.Fatalf("expected 2 DataFlowEdges, got %d: %+v", len(structure.DataFlowEdges), structure.DataFlowEdges)
	}

	edgeSet := make(map[string]bool)
	for _, e := range structure.DataFlowEdges {
		edgeSet[e.From+"|"+e.To+"|"+e.Key] = true
	}

	if !edgeSet["A|C|alpha"] {
		t.Errorf("missing edge A→C on key 'alpha'")
	}
	if !edgeSet["B|C|beta"] {
		t.Errorf("missing edge B→C on key 'beta'")
	}
}

// TestStructure_NodeInfoIncludesInputOutputKeys verifies that NodeInfo includes
// InputKeys and OutputKeys populated from data-flow declarations.
func TestStructure_NodeInfoIncludesInputOutputKeys(t *testing.T) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := g.Node("entry", func(_ context.Context, s State) (State, error) {
		return State{"k1": "v1", "k2": "v2"}, nil
	}, In(), Out("k1", "k2")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("consumer", func(_ context.Context, s State) (State, error) {
		return State{"out": "done"}, nil
	}, In("k1", "k2"), Out("out")); err != nil {
		t.Fatal(err)
	}
	g.Start("entry")

	structure := g.Structure()

	// Find entry node info.
	var entryInfo, consumerInfo *NodeInfo
	for i := range structure.Nodes {
		switch structure.Nodes[i].ID {
		case "entry":
			entryInfo = &structure.Nodes[i]
		case "consumer":
			consumerInfo = &structure.Nodes[i]
		}
	}

	if entryInfo == nil {
		t.Fatal("entry node not found in structure")
	}
	if consumerInfo == nil {
		t.Fatal("consumer node not found in structure")
	}

	// Verify entry node keys.
	if len(entryInfo.InputKeys) != 0 {
		t.Errorf("entry InputKeys: expected empty, got %v", entryInfo.InputKeys)
	}
	if len(entryInfo.OutputKeys) != 2 {
		t.Errorf("entry OutputKeys: expected 2, got %d: %v", len(entryInfo.OutputKeys), entryInfo.OutputKeys)
	}
	outputSet := make(map[string]bool)
	for _, k := range entryInfo.OutputKeys {
		outputSet[k] = true
	}
	if !outputSet["k1"] || !outputSet["k2"] {
		t.Errorf("entry OutputKeys missing expected keys, got %v", entryInfo.OutputKeys)
	}

	// Verify consumer node keys.
	if len(consumerInfo.InputKeys) != 2 {
		t.Errorf("consumer InputKeys: expected 2, got %d: %v", len(consumerInfo.InputKeys), consumerInfo.InputKeys)
	}
	inputSet := make(map[string]bool)
	for _, k := range consumerInfo.InputKeys {
		inputSet[k] = true
	}
	if !inputSet["k1"] || !inputSet["k2"] {
		t.Errorf("consumer InputKeys missing expected keys, got %v", consumerInfo.InputKeys)
	}
	if len(consumerInfo.OutputKeys) != 1 || consumerInfo.OutputKeys[0] != "out" {
		t.Errorf("consumer OutputKeys: expected [out], got %v", consumerInfo.OutputKeys)
	}
}

// TestStructure_EmptyGraphReturnsEmptyDataFlowEdges verifies that an empty graph
// (only entry node, no dependencies) returns an empty DataFlowEdges slice (not nil).
func TestStructure_EmptyGraphReturnsEmptyDataFlowEdges(t *testing.T) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := g.Node("entry", func(_ context.Context, s State) (State, error) {
		return State{"done": true}, nil
	}, In(), Out("done")); err != nil {
		t.Fatal(err)
	}
	g.Start("entry")

	structure := g.Structure()

	// DataFlowEdges should be an empty slice, not nil.
	if structure.DataFlowEdges == nil {
		t.Fatal("DataFlowEdges should not be nil, expected empty slice")
	}
	if len(structure.DataFlowEdges) != 0 {
		t.Fatalf("expected 0 DataFlowEdges, got %d: %+v", len(structure.DataFlowEdges), structure.DataFlowEdges)
	}
}

// TestStructure_BFSLayerAssignment verifies that bfsOrder assigns correct layers
// based on data-flow topology.
func TestStructure_BFSLayerAssignment(t *testing.T) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	// Linear chain: A→B→C should have layers 0, 1, 2.
	if _, err := g.Node("A", func(_ context.Context, s State) (State, error) {
		return State{"x": "a"}, nil
	}, In(), Out("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("B", func(_ context.Context, s State) (State, error) {
		return State{"y": "b"}, nil
	}, In("x"), Out("y")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Node("C", func(_ context.Context, s State) (State, error) {
		return State{"z": "c"}, nil
	}, In("y"), Out("z")); err != nil {
		t.Fatal(err)
	}
	g.Start("A")

	structure := g.Structure()

	layerMap := make(map[string]int)
	for _, n := range structure.Nodes {
		layerMap[n.ID] = n.Layer
	}

	if layerMap["A"] != 0 {
		t.Errorf("A layer: expected 0, got %d", layerMap["A"])
	}
	if layerMap["B"] != 1 {
		t.Errorf("B layer: expected 1, got %d", layerMap["B"])
	}
	if layerMap["C"] != 2 {
		t.Errorf("C layer: expected 2, got %d", layerMap["C"])
	}
}
