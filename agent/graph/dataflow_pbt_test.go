package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"pgregory.net/rapid"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Property-Based Tests for Graph Data-Flow Scheduling
// ═══════════════════════════════════════════════════════════════════════════════

// ── Property 1: Data-Flow Scheduling Order ───────────────────────────────────
// Feature: graph-dataflow-scheduling, Property 1: Data-Flow Scheduling Order
//
// For any valid DAG of nodes with data-flow declarations and any initial state,
// every node executes only after all its declared input keys are present in the
// readiness set, and the entry node always executes first regardless of its
// input declarations.
//
// **Validates: Requirements 3.1, 3.3, 3.4, 3.5, 11.1, 11.4**

func TestProperty_1_DataFlowSchedulingOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numNodes := rapid.IntRange(2, 8).Draw(rt, "numNodes")

		// Generate unique key names for each node's output.
		keys := make([]string, numNodes)
		for i := range keys {
			keys[i] = fmt.Sprintf("key_%d", i)
		}

		// Track execution order.
		var mu sync.Mutex
		var executionOrder []string

		// Build graph with random DAG topology.
		// Node 0 is always the entry. Edges only go from lower to higher index (DAG).
		g, err := New[State](WithMaxIterations(100))
		if err != nil {
			rt.Fatal(err)
		}

		type nodeSpec struct {
			name       string
			outputKeys []string
			inputKeys  []string
		}
		specs := make([]nodeSpec, numNodes)

		for i := 0; i < numNodes; i++ {
			name := fmt.Sprintf("node_%d", i)
			outputKey := keys[i]

			var inputKeys []string
			if i > 0 {
				// Each non-entry node depends on at least one predecessor.
				// Pick a random subset of predecessors (at least one).
				numDeps := rapid.IntRange(1, i).Draw(rt, fmt.Sprintf("numDeps_%d", i))
				// Pick numDeps unique predecessors from [0, i) using sampling.
				chosen := make(map[int]bool)
				for len(chosen) < numDeps {
					p := rapid.IntRange(0, i-1).Draw(rt, fmt.Sprintf("pred_%d_%d", i, len(chosen)))
					chosen[p] = true
				}
				for p := range chosen {
					inputKeys = append(inputKeys, keys[p])
				}
			}

			specs[i] = nodeSpec{name: name, outputKeys: []string{outputKey}, inputKeys: inputKeys}

			nodeName := name
			outKey := outputKey
			if _, err := g.Node(nodeName, func(_ context.Context, s State) (State, error) {
				mu.Lock()
				executionOrder = append(executionOrder, nodeName)
				mu.Unlock()
				out := CopyState(s)
				out[outKey] = true
				return out, nil
			}, In(inputKeys...), Out(outKey)); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start(specs[0].name)

		_, err = g.Run(context.Background(), State{})
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Verify entry node is always first.
		if len(executionOrder) == 0 {
			rt.Fatal("no nodes executed")
		}
		if executionOrder[0] != specs[0].name {
			rt.Fatalf("entry node %q should execute first, got %q", specs[0].name, executionOrder[0])
		}

		// Verify scheduling order: for each node, all its input key producers
		// must have executed before it.
		executedBefore := make(map[string]bool)
		producedKeys := make(map[string]bool)
		for _, nodeName := range executionOrder {
			// Find this node's spec.
			var spec nodeSpec
			for _, s := range specs {
				if s.name == nodeName {
					spec = s
					break
				}
			}
			// Verify all input keys were produced before this node executed.
			for _, inputKey := range spec.inputKeys {
				if !producedKeys[inputKey] {
					rt.Fatalf("node %q executed but input key %q was not yet produced", nodeName, inputKey)
				}
			}
			executedBefore[nodeName] = true
			for _, outKey := range spec.outputKeys {
				producedKeys[outKey] = true
			}
		}
	})
}

// ── Property 2: Readiness Set Monotonicity ───────────────────────────────────
// Feature: graph-dataflow-scheduling, Property 2: Readiness Set Monotonicity
//
// For any data-flow graph execution, the readiness set only grows — keys are
// never removed. After each node completes, its declared output keys are added
// to the readiness set, and the resulting set is a superset of the previous
// readiness set.
//
// **Validates: Requirements 3.3, 3.4**

func TestProperty_2_ReadinessSetMonotonicity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numNodes := rapid.IntRange(2, 8).Draw(rt, "numNodes")

		keys := make([]string, numNodes)
		for i := range keys {
			keys[i] = fmt.Sprintf("key_%d", i)
		}

		// Track readiness set snapshots via event hook.
		var mu sync.Mutex
		var readinessSets []map[string]bool

		g, err := New[State](WithMaxIterations(100))
		if err != nil {
			rt.Fatal(err)
		}

		// Build a linear chain for simplicity (guarantees sequential execution).
		for i := 0; i < numNodes; i++ {
			name := fmt.Sprintf("node_%d", i)
			outputKey := keys[i]
			var inputKeys []string
			if i > 0 {
				inputKeys = []string{keys[i-1]}
			}

			outKey := outputKey
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[outKey] = true
				// After this node runs, capture the readiness set state
				// by recording which keys are present in state.
				mu.Lock()
				snapshot := make(map[string]bool)
				for k := range out {
					snapshot[k] = true
				}
				readinessSets = append(readinessSets, snapshot)
				mu.Unlock()
				return out, nil
			}, In(inputKeys...), Out(outKey)); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start("node_0")

		_, err = g.Run(context.Background(), State{})
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Verify monotonicity: each snapshot is a superset of the previous.
		for i := 1; i < len(readinessSets); i++ {
			prev := readinessSets[i-1]
			curr := readinessSets[i]
			for k := range prev {
				if !curr[k] {
					rt.Fatalf("readiness set shrank: key %q present at step %d but missing at step %d", k, i-1, i)
				}
			}
		}
	})
}

// ── Property 3: Cycle Detection Completeness ─────────────────────────────────
// Feature: graph-dataflow-scheduling, Property 3: Cycle Detection Completeness
//
// For any graph where the data-flow declarations form a cycle (node A's output
// feeds node B's input, and node B's output feeds node A's input, directly or
// transitively), Run() shall return a GraphValidationError before any node executes.
//
// **Validates: Requirements 3.6, 9.1**

func TestProperty_3_CycleDetectionCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numNodes := rapid.IntRange(2, 8).Draw(rt, "numNodes")

		// Build a valid DAG first (edges only from lower to higher index).
		keys := make([]string, numNodes)
		for i := range keys {
			keys[i] = fmt.Sprintf("key_%d", i)
		}

		g, err := New[State](WithMaxIterations(100))
		if err != nil {
			rt.Fatal(err)
		}

		// Register entry node.
		if _, err := g.Node("entry", noop, In("entry_out"), Out()); err != nil {
			rt.Fatal(err)
		}

		// Register chain nodes that form a valid DAG.
		for i := 0; i < numNodes; i++ {
			name := fmt.Sprintf("node_%d", i)
			outputKey := keys[i]
			var inputKeys []string
			if i == 0 {
				inputKeys = []string{"entry_out"}
			} else {
				inputKeys = []string{keys[i-1]}
			}
			if _, err := g.Node(name, noop, In(outputKey), Out(inputKeys...)); err != nil {
				rt.Fatal(err)
			}
		}

		// Now add a back-edge to create a cycle: last node's output feeds into
		// a random earlier node's input. We do this by adding a new node that
		// creates the cycle.
		cycleTarget := rapid.IntRange(0, numNodes-1).Draw(rt, "cycleTarget")
		// Add a cycle node that depends on the last node's output and produces
		// the target node's input key (which is already produced by another node).
		// Instead, we create a mutual dependency by adding a node that depends on
		// the last key and produces a key that an earlier node depends on.
		// Simplest approach: add a node whose output is consumed by an existing node
		// and whose input comes from a later node.
		cycleName := "cycle_node"
		cycleOutputKey := fmt.Sprintf("key_%d", cycleTarget) // conflicts with existing!

		// Actually, let's create a clean cycle by making a new graph with explicit cycle.
		g2, err := New[State](WithMaxIterations(100))
		if err != nil {
			rt.Fatal(err)
		}

		// Entry node with no dependencies.
		if _, err := g2.Node("entry", noop, In(), Out("entry_out")); err != nil {
			rt.Fatal(err)
		}

		// Create a cycle among numNodes nodes (excluding entry).
		// node_0 outputs key_0, inputs key_{numNodes-1} (from last node)
		// node_1 outputs key_1, inputs key_0
		// ...
		// node_{n-1} outputs key_{n-1}, inputs key_{n-2}
		// This creates a cycle: 0 → 1 → ... → n-1 → 0
		for i := 0; i < numNodes; i++ {
			name := fmt.Sprintf("cyc_%d", i)
			outputKey := fmt.Sprintf("cyc_key_%d", i)
			var inputKey string
			if i == 0 {
				inputKey = fmt.Sprintf("cyc_key_%d", numNodes-1) // back-edge
			} else {
				inputKey = fmt.Sprintf("cyc_key_%d", i-1)
			}
			if _, err := g2.Node(name, noop, In(inputKey), Out(outputKey)); err != nil {
				rt.Fatal(err)
			}
		}
		g2.Start("entry")

		_, err = g2.Run(context.Background(), State{})
		if err == nil {
			rt.Fatal("expected error for cycle, got nil")
		}

		var ve *GraphValidationError
		if !errors.As(err, &ve) {
			rt.Fatalf("expected GraphValidationError, got %T: %v", err, err)
		}

		// Suppress unused variable warnings.
		_ = cycleName
		_ = cycleOutputKey
	})
}

// ── Property 4: Output Key Uniqueness Validation ─────────────────────────────
// Feature: graph-dataflow-scheduling, Property 4: Output Key Uniqueness Validation
//
// For any graph where two or more nodes declare the same output key, Run() shall
// return a GraphValidationError identifying the conflicting nodes and the
// duplicate key.
//
// **Validates: Requirements 9.4**

func TestProperty_4_OutputKeyUniquenessValidation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numNodes := rapid.IntRange(2, 8).Draw(rt, "numNodes")

		g, err := New[State](WithMaxIterations(100))
		if err != nil {
			rt.Fatal(err)
		}

		// Entry node.
		if _, err := g.Node("entry", noop, In("entry_out"), Out()); err != nil {
			rt.Fatal(err)
		}

		// Generate a shared key that two nodes will both declare as output.
		sharedKey := rapid.StringMatching("[a-z]{3,8}").Draw(rt, "sharedKey")

		// Pick two distinct node indices to have the duplicate output key.
		nodeA := rapid.IntRange(0, numNodes-1).Draw(rt, "nodeA")
		nodeB := rapid.IntRange(0, numNodes-1).Draw(rt, "nodeB")
		for nodeB == nodeA {
			nodeB = rapid.IntRange(0, numNodes-1).Draw(rt, "nodeB_retry")
		}

		for i := 0; i < numNodes; i++ {
			name := fmt.Sprintf("dup_%d", i)
			var outputKey string
			if i == nodeA || i == nodeB {
				outputKey = sharedKey
			} else {
				outputKey = fmt.Sprintf("unique_%d", i)
			}
			if _, err := g.Node(name, noop, In(outputKey), Out("entry_out")); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start("entry")

		_, err = g.Run(context.Background(), State{})
		if err == nil {
			rt.Fatal("expected error for duplicate output key, got nil")
		}

		var ve *GraphValidationError
		if !errors.As(err, &ve) {
			rt.Fatalf("expected GraphValidationError, got %T: %v", err, err)
		}
	})
}

// ── Property 5: Input Key Satisfiability Validation ──────────────────────────
// Feature: graph-dataflow-scheduling, Property 5: Input Key Satisfiability Validation
//
// For any graph where a node declares an input key that is neither present in
// the initial state nor declared as an output key by any other node, Run() shall
// return a GraphValidationError identifying the node and the missing key.
//
// **Validates: Requirements 9.2, 9.3**

func TestProperty_5_InputKeySatisfiabilityValidation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		g, err := New[State](WithMaxIterations(100))
		if err != nil {
			rt.Fatal(err)
		}

		// Entry node.
		if _, err := g.Node("entry", noop, In("entry_out"), Out()); err != nil {
			rt.Fatal(err)
		}

		// Generate a missing key that no node produces and is not in initial state.
		missingKey := rapid.StringMatching("[a-z]{3,8}").Draw(rt, "missingKey")
		// Ensure it doesn't collide with "entry_out".
		for missingKey == "entry_out" {
			missingKey = rapid.StringMatching("[a-z]{3,8}").Draw(rt, "missingKey_retry")
		}

		// Add a node that requires the missing key.
		nodeName := rapid.StringMatching("[a-z]{3,8}").Draw(rt, "nodeName")
		for nodeName == "entry" || nodeName == missingKey {
			nodeName = rapid.StringMatching("[a-z]{3,8}").Draw(rt, "nodeName_retry")
		}

		if _, err := g.Node(nodeName, noop, In(nodeName+"_out"), Out(missingKey)); err != nil {
			rt.Fatal(err)
		}
		g.Start("entry")

		_, err = g.Run(context.Background(), State{})
		if err == nil {
			rt.Fatal("expected error for unsatisfiable input key, got nil")
		}

		var ve *GraphValidationError
		if !errors.As(err, &ve) {
			rt.Fatalf("expected GraphValidationError, got %T: %v", err, err)
		}
	})
}

// ── Property 6: Concurrent Merge Determinism ─────────────────────────────────
// Feature: graph-dataflow-scheduling, Property 6: Concurrent Merge Determinism
//
// For any set of concurrently executing data-flow nodes, the merge result is
// deterministic: each node receives an isolated state copy, results are merged
// using mergeDiff in alphabetical node-name order, and running the same graph
// with the same initial state always produces the same final state.
//
// **Validates: Requirements 10.1, 10.2, 10.3, 10.4**

func TestProperty_6_ConcurrentMergeDeterminism(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a diamond-like graph: entry → (concurrent nodes) → sink.
		numConcurrent := rapid.IntRange(2, 5).Draw(rt, "numConcurrent")

		// Build graph.
		buildGraph := func() (*Graph[State], error) {
			g, err := New[State](WithMaxIterations(100))
			if err != nil {
				return nil, err
			}

			// Entry node produces "entry_out".
			if _, err := g.Node("entry", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["entry_out"] = true
				return out, nil
			}, In(), Out("entry_out")); err != nil {
				return nil, err
			}

			// Concurrent nodes: each depends on "entry_out" and produces its own key.
			concKeys := make([]string, numConcurrent)
			for i := 0; i < numConcurrent; i++ {
				name := fmt.Sprintf("conc_%d", i)
				outKey := fmt.Sprintf("conc_out_%d", i)
				concKeys[i] = outKey
				idx := i
				if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
					out := CopyState(s)
					out[outKey] = fmt.Sprintf("value_%d", idx)
					return out, nil
				}, In("entry_out"), Out(outKey)); err != nil {
					return nil, err
				}
			}

			// Sink node depends on all concurrent outputs.
			if _, err := g.Node("sink", func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out["sink_out"] = true
				return out, nil
			}, In(concKeys...), Out("sink_out")); err != nil {
				return nil, err
			}

			g.Start("entry")
			return g, nil
		}

		// Run the graph 5 times and verify identical final state.
		var firstState State
		for run := 0; run < 5; run++ {
			g, err := buildGraph()
			if err != nil {
				rt.Fatal(err)
			}
			result, err := g.Run(context.Background(), State{"init": "data"})
			if err != nil {
				rt.Fatalf("run %d failed: %v", run, err)
			}
			if run == 0 {
				firstState = result.State
			} else {
				// Compare with first run.
				if len(result.State) != len(firstState) {
					rt.Fatalf("run %d: state length %d != first run %d", run, len(result.State), len(firstState))
				}
				for k, v := range firstState {
					if fmt.Sprintf("%v", result.State[k]) != fmt.Sprintf("%v", v) {
						rt.Fatalf("run %d: state[%q] = %v, first run = %v", run, k, result.State[k], v)
					}
				}
			}
		}
	})
}

// ── Property 7: Data-Flow Edge Completeness ──────────────────────────────────
// Feature: graph-dataflow-scheduling, Property 7: Data-Flow Edge Completeness
//
// For any data-flow graph, Structure() returns exactly one DataFlowEdge for
// every (producer, consumer, key) triple where the producer declares the key as
// an output and the consumer declares it as an input, and no spurious edges exist.
//
// **Validates: Requirements 5.1, 5.2, 5.3, 5.4**

func TestProperty_7_DataFlowEdgeCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		numNodes := rapid.IntRange(2, 8).Draw(rt, "numNodes")

		keys := make([]string, numNodes)
		for i := range keys {
			keys[i] = fmt.Sprintf("key_%d", i)
		}

		g, err := New[State](WithMaxIterations(100))
		if err != nil {
			rt.Fatal(err)
		}

		type nodeDecl struct {
			name       string
			outputKeys []string
			inputKeys  []string
		}
		decls := make([]nodeDecl, numNodes)

		// Build a random DAG (edges only from lower to higher index).
		for i := 0; i < numNodes; i++ {
			name := fmt.Sprintf("node_%d", i)
			outputKey := keys[i]
			var inputKeys []string
			if i > 0 {
				numDeps := rapid.IntRange(1, i).Draw(rt, fmt.Sprintf("deps_%d", i))
				chosen := make(map[int]bool)
				for len(chosen) < numDeps {
					p := rapid.IntRange(0, i-1).Draw(rt, fmt.Sprintf("pred_%d_%d", i, len(chosen)))
					chosen[p] = true
				}
				for p := range chosen {
					inputKeys = append(inputKeys, keys[p])
				}
			}
			decls[i] = nodeDecl{name: name, outputKeys: []string{outputKey}, inputKeys: inputKeys}
			if _, err := g.Node(name, noop, In(inputKeys...), Out(outputKey)); err != nil {
				rt.Fatal(err)
			}
		}
		g.Start(decls[0].name)

		// Get structure.
		structure := g.Structure()

		// Build expected edges: for each consumer's input key, find the producer.
		outputKeyToProducer := make(map[string]string)
		for _, d := range decls {
			for _, k := range d.outputKeys {
				outputKeyToProducer[k] = d.name
			}
		}

		type edgeTriple struct {
			from, to, key string
		}
		expectedEdges := make(map[edgeTriple]bool)
		for _, d := range decls {
			for _, inputKey := range d.inputKeys {
				if producer, ok := outputKeyToProducer[inputKey]; ok {
					expectedEdges[edgeTriple{producer, d.name, inputKey}] = true
				}
			}
		}

		// Verify actual edges match expected.
		actualEdges := make(map[edgeTriple]bool)
		for _, e := range structure.DataFlowEdges {
			triple := edgeTriple{e.From, e.To, e.Key}
			if actualEdges[triple] {
				rt.Fatalf("duplicate edge: %v", triple)
			}
			actualEdges[triple] = true
		}

		// Check no missing edges.
		for e := range expectedEdges {
			if !actualEdges[e] {
				rt.Fatalf("missing edge: from=%q to=%q key=%q", e.from, e.to, e.key)
			}
		}

		// Check no spurious edges.
		for e := range actualEdges {
			if !expectedEdges[e] {
				rt.Fatalf("spurious edge: from=%q to=%q key=%q", e.from, e.to, e.key)
			}
		}
	})
}

// ── Property 8: Checkpoint Readiness Set Preservation ────────────────────────
// Feature: graph-dataflow-scheduling, Property 8: Checkpoint Readiness Set Preservation
//
// For any data-flow graph that is interrupted (via InterruptAfter), the
// checkpoint contains the current readiness set, and resuming from that
// checkpoint restores the readiness set and continues scheduling from the
// correct point — producing the same final state as an uninterrupted execution.
//
// **Validates: Requirements 7.4, 7.5**

func TestProperty_8_CheckpointReadinessSetPreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a linear chain of 3-6 nodes.
		numNodes := rapid.IntRange(3, 6).Draw(rt, "numNodes")

		// Build a function that creates the graph (reusable for both runs).
		type nodeInfo struct {
			name      string
			outputKey string
			inputKey  string // empty for entry
		}
		nodes := make([]nodeInfo, numNodes)
		for i := 0; i < numNodes; i++ {
			nodes[i] = nodeInfo{
				name:      fmt.Sprintf("node_%d", i),
				outputKey: fmt.Sprintf("out_%d", i),
			}
			if i > 0 {
				nodes[i].inputKey = fmt.Sprintf("out_%d", i-1)
			}
		}

		// Run 1: Uninterrupted execution to get expected final state.
		g1, err := New[State](WithMaxIterations(100))
		if err != nil {
			rt.Fatal(err)
		}
		for _, n := range nodes {
			ni := n
			var inputKeys []string
			if ni.inputKey != "" {
				inputKeys = []string{ni.inputKey}
			}
			if _, err := g1.Node(ni.name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[ni.outputKey] = ni.name + "_done"
				return out, nil
			}, In(inputKeys...), Out(ni.outputKey)); err != nil {
				rt.Fatal(err)
			}
		}
		g1.Start(nodes[0].name)

		expectedResult, err := g1.Run(context.Background(), State{"init": "val"})
		if err != nil {
			rt.Fatalf("uninterrupted run failed: %v", err)
		}

		// Run 2: Interrupted execution + resume.
		// Pick a random node (not the last) to interrupt after.
		interruptIdx := rapid.IntRange(0, numNodes-2).Draw(rt, "interruptIdx")

		cp := &mockCheckpointer{}
		g2, err := New[State](WithMaxIterations(100), WithCheckpointer(cp))
		if err != nil {
			rt.Fatal(err)
		}
		for _, n := range nodes {
			ni := n
			var inputKeys []string
			if ni.inputKey != "" {
				inputKeys = []string{ni.inputKey}
			}
			if _, err := g2.Node(ni.name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[ni.outputKey] = ni.name + "_done"
				return out, nil
			}, In(inputKeys...), Out(ni.outputKey)); err != nil {
				rt.Fatal(err)
			}
		}
		g2.Start(nodes[0].name)
		if err := g2.InterruptAfter(nodes[interruptIdx].name); err != nil {
			rt.Fatal(err)
		}

		threadID := "test-thread"
		_, err = g2.Run(context.Background(), State{"init": "val"}, WithThreadID(threadID))

		var intErr *GraphInterruptError
		if !errors.As(err, &intErr) {
			rt.Fatalf("expected GraphInterruptError, got %T: %v", err, err)
		}

		// Verify checkpoint has readiness set.
		lastCP := cp.saved[len(cp.saved)-1]
		if lastCP.ReadinessSet == nil {
			rt.Fatal("checkpoint ReadinessSet is nil")
		}

		// Resume execution (remove the interrupt for resume).
		// We need to clear the interrupt so resume can complete.
		delete(g2.interruptAfter, nodes[interruptIdx].name)

		resumeResult, err := g2.Resume(context.Background(), threadID, nil)
		if err != nil {
			rt.Fatalf("resume failed: %v", err)
		}

		// Verify same final state as uninterrupted execution.
		for k, v := range expectedResult.State {
			if fmt.Sprintf("%v", resumeResult.State[k]) != fmt.Sprintf("%v", v) {
				rt.Fatalf("state mismatch after resume: key=%q expected=%v got=%v", k, v, resumeResult.State[k])
			}
		}
		for k, v := range resumeResult.State {
			if fmt.Sprintf("%v", expectedResult.State[k]) != fmt.Sprintf("%v", v) {
				rt.Fatalf("state mismatch after resume (extra key): key=%q expected=%v got=%v", k, expectedResult.State[k], v)
			}
		}
	})
}

// ── Property 9: Conditional Data-Flow Gating ─────────────────────────────────
// Feature: graph-dataflow-scheduling, Property 9: Conditional Data-Flow Gating
//
// For any data-flow graph where a node conditionally writes an output key
// (sometimes writes it, sometimes doesn't), downstream nodes that declare that
// key as an input shall execute only when the key is actually written, and the
// graph shall terminate when no more nodes can become ready.
//
// **Validates: Requirements 4.6, 11.2**

func TestProperty_9_ConditionalDataFlowGating(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate whether the conditional node writes its output key.
		writeKey := rapid.Bool().Draw(rt, "writeKey")

		var mu sync.Mutex
		var executedNodes []string

		g, err := New[State](WithMaxIterations(100))
		if err != nil {
			rt.Fatal(err)
		}

		// Entry node always writes "entry_out".
		if _, err := g.Node("entry", func(_ context.Context, s State) (State, error) {
			mu.Lock()
			executedNodes = append(executedNodes, "entry")
			mu.Unlock()
			out := CopyState(s)
			out["entry_out"] = true
			return out, nil
		}, In(), Out("entry_out")); err != nil {
			rt.Fatal(err)
		}

		// Conditional node: declares "cond_out" as output but only writes it
		// when writeKey is true.
		if _, err := g.Node("conditional", func(_ context.Context, s State) (State, error) {
			mu.Lock()
			executedNodes = append(executedNodes, "conditional")
			mu.Unlock()
			out := CopyState(s)
			if writeKey {
				out["cond_out"] = true
			}
			return out, nil
		}, In("entry_out"), Out("cond_out")); err != nil {
			rt.Fatal(err)
		}

		// Downstream node depends on "cond_out".
		if _, err := g.Node("downstream", func(_ context.Context, s State) (State, error) {
			mu.Lock()
			executedNodes = append(executedNodes, "downstream")
			mu.Unlock()
			out := CopyState(s)
			out["downstream_out"] = true
			return out, nil
		}, In("cond_out"), Out("downstream_out")); err != nil {
			rt.Fatal(err)
		}

		g.Start("entry")

		_, err = g.Run(context.Background(), State{})
		if err != nil {
			rt.Fatalf("Run failed: %v", err)
		}

		// Verify entry and conditional always execute.
		hasEntry := false
		hasConditional := false
		hasDownstream := false
		for _, n := range executedNodes {
			switch n {
			case "entry":
				hasEntry = true
			case "conditional":
				hasConditional = true
			case "downstream":
				hasDownstream = true
			}
		}

		if !hasEntry {
			rt.Fatal("entry node should always execute")
		}
		if !hasConditional {
			rt.Fatal("conditional node should always execute")
		}

		// Downstream should execute only when key was written.
		if writeKey && !hasDownstream {
			rt.Fatal("downstream should execute when conditional writes the key")
		}
		if !writeKey && hasDownstream {
			rt.Fatal("downstream should NOT execute when conditional does not write the key")
		}
	})
}

// ── Property 10: Keys Helper Metadata Extraction ─────────────────────────────
// Feature: graph-dataflow-scheduling, Property 10: Keys Helper Metadata Extraction
//
// For any call to Keys(outputKey, inputKeys...), the resulting
// AgentNodeAccessor[State] stores outputKey as the sole output key and all
// inputKeys as input keys, and when used with g.Agent(), the graph stores
// matching DataFlowMeta for that node.
//
// **Validates: Requirements 2.1, 2.2**

func TestProperty_10_KeysHelperMetadataExtraction(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random output key.
		outputKey := rapid.StringMatching("[a-z]{3,8}").Draw(rt, "outputKey")

		// Generate random input keys (1-5 keys).
		numInputKeys := rapid.IntRange(1, 5).Draw(rt, "numInputKeys")
		inputKeys := make([]string, numInputKeys)
		usedKeys := map[string]bool{outputKey: true}
		for i := 0; i < numInputKeys; i++ {
			key := rapid.StringMatching("[a-z]{3,8}").Draw(rt, fmt.Sprintf("inputKey_%d", i))
			// Ensure uniqueness.
			for usedKeys[key] {
				key = rapid.StringMatching("[a-z]{3,8}").Draw(rt, fmt.Sprintf("inputKey_%d_retry", i))
			}
			usedKeys[key] = true
			inputKeys[i] = key
		}

		// Call Keys() helper.
		accessor := Keys(outputKey, inputKeys...)

		// Verify accessor stores correct output keys.
		if len(accessor.OutputKeys) != 1 {
			rt.Fatalf("expected 1 output key, got %d", len(accessor.OutputKeys))
		}
		if accessor.OutputKeys[0] != outputKey {
			rt.Fatalf("expected output key %q, got %q", outputKey, accessor.OutputKeys[0])
		}

		// Verify accessor stores correct input keys.
		if len(accessor.InputKeys) != len(inputKeys) {
			rt.Fatalf("expected %d input keys, got %d", len(inputKeys), len(accessor.InputKeys))
		}
		sort.Strings(inputKeys)
		sortedAccessorKeys := make([]string, len(accessor.InputKeys))
		copy(sortedAccessorKeys, accessor.InputKeys)
		sort.Strings(sortedAccessorKeys)
		for i, k := range sortedAccessorKeys {
			if k != inputKeys[i] {
				rt.Fatalf("input key mismatch at %d: expected %q, got %q", i, inputKeys[i], k)
			}
		}
	})
}
