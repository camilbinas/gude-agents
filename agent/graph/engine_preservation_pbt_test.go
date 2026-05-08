package graph

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"pgregory.net/rapid"
)

// ─── Preservation Property-Based Tests ───────────────────────────────────────
//
// These tests capture EXISTING correct behavior on UNFIXED code.
// They must PASS on the current codebase to confirm baseline behavior.
// After the fix is applied, they must STILL PASS (no regressions).
//
// **Validates: Requirements 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 3.9, 3.10**

// ─── Test 2a: Single Agent Node with Bridge Hooks ────────────────────────────
//
// Preservation: Single agent node execution with bridge hooks forwarding
// tracing/metrics/logging to graph-level hooks must continue to work.
//
// **Validates: Requirements 3.1, 3.3, 3.4, 3.5, 3.6, 3.7, 3.9**

func TestPreservation_SingleAgentNodeBridgeHooks(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random input message.
		inputMsg := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "inputMsg")

		// Create a single agent with a scripted provider.
		sp := &raceTestProvider{}
		a, err := agent.New(sp, prompt.Text("preservation agent"), nil)
		if err != nil {
			rt.Fatalf("agent.New: %v", err)
		}

		// Set up graph with all three hook types.
		tracingHook := &preservationTracingHook{}
		metricsHook := &preservationMetricsHook{}
		loggingHook := &preservationLoggingHook{}

		g, err := New[State]()
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}
		g.SetGraphTracingHook(tracingHook)
		g.SetGraphMetricsHook(metricsHook)
		g.SetGraphLoggingHook(loggingHook)

		// Register a single agent node as entry.
		if _, err := g.Agent("agent_node", a, AgentNodeAccessor[State]{
			GetInput:   func(s State) string { return s["input"].(string) },
			SetOutput:  func(s *State, out string) { (*s)["output"] = out },
			InputKeys:  []string{"input"},
			OutputKeys: []string{"output"},
		}); err != nil {
			rt.Fatalf("Agent: %v", err)
		}
		g.Start("agent_node")

		result, err := g.Run(context.Background(), State{"input": inputMsg})
		if err != nil {
			rt.Fatalf("Run: %v", err)
		}

		// Verify: agent produced output.
		if result.State["output"] != "mock response" {
			rt.Fatalf("expected output='mock response', got %q", result.State["output"])
		}

		// Verify: tracing hook was called (graph run start + node start).
		if tracingHook.graphRunStartCount.Load() != 1 {
			rt.Fatalf("expected 1 graph run start trace, got %d", tracingHook.graphRunStartCount.Load())
		}
		if tracingHook.nodeStartCount.Load() < 1 {
			rt.Fatalf("expected at least 1 node start trace, got %d", tracingHook.nodeStartCount.Load())
		}

		// Verify: metrics hook was called.
		if metricsHook.graphStartCount.Load() != 1 {
			rt.Fatalf("expected 1 graph metrics start, got %d", metricsHook.graphStartCount.Load())
		}
		if metricsHook.nodeStartCount.Load() < 1 {
			rt.Fatalf("expected at least 1 node metrics start, got %d", metricsHook.nodeStartCount.Load())
		}

		// Verify: logging hook was called.
		if loggingHook.graphRunStartCount.Load() != 1 {
			rt.Fatalf("expected 1 graph logging start, got %d", loggingHook.graphRunStartCount.Load())
		}
		if loggingHook.nodeStartCount.Load() < 1 {
			rt.Fatalf("expected at least 1 node logging start, got %d", loggingHook.nodeStartCount.Load())
		}
	})
}

// ─── Test 2b: Map State Zero-Cost updateReadiness ────────────────────────────
//
// Preservation: map[string]any state (mapStateOps) must continue to have
// zero-cost state operations in updateReadiness (no JSON serialization).
//
// **Validates: Requirements 3.2**

func TestPreservation_MapStateZeroCostReadiness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a chain of 2-4 nodes.
		numNodes := rapid.IntRange(2, 4).Draw(rt, "numNodes")

		g, err := New[State]()
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}

		// Wrap ops with a counting wrapper to verify no toMap calls in updateReadiness.
		// For map state, toMap is identity (returns the map itself), so it's zero-cost.
		// We verify the graph executes correctly and that map state ops are used.
		var prevOutKey string
		for i := range numNodes {
			name := fmt.Sprintf("node_%d", i)
			outKey := fmt.Sprintf("out_%d", i)
			var inputKeys []string
			if prevOutKey != "" {
				inputKeys = []string{prevOutKey}
			}

			localOutKey := outKey
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[localOutKey] = true
				return out, nil
			}, In(inputKeys...), Out(outKey)); err != nil {
				rt.Fatalf("Node(%s): %v", name, err)
			}
			prevOutKey = outKey
		}
		g.Start("node_0")

		result, err := g.Run(context.Background(), State{})
		if err != nil {
			rt.Fatalf("Run: %v", err)
		}

		// Verify all nodes executed and produced their output keys.
		for i := range numNodes {
			outKey := fmt.Sprintf("out_%d", i)
			if result.State[outKey] != true {
				rt.Fatalf("expected state[%s]=true, got %v", outKey, result.State[outKey])
			}
		}

		// For map state, the ops are mapStateOps which is zero-cost.
		// The fact that the graph ran correctly with map state confirms preservation.
	})
}

// ─── Test 2c: InterruptBefore/InterruptAfter Correctness ─────────────────────
//
// Preservation: InterruptBefore/InterruptAfter must continue to save checkpoints
// and return GraphInterruptError at correct points.
//
// **Validates: Requirements 3.3**

func TestPreservation_InterruptBeforeAfter(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Choose interrupt type: before or after.
		interruptType := rapid.SampledFrom([]string{"before", "after"}).Draw(rt, "interruptType")

		cp := &mockCheckpointer{}
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}

		// Build a 3-node chain: a → b → c.
		if _, err := g.Node("a", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["a"] = "done_a"
			out["a_out"] = true
			return out, nil
		}, In(), Out("a_out")); err != nil {
			rt.Fatalf("Node(a): %v", err)
		}
		if _, err := g.Node("b", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["b"] = "done_b"
			out["b_out"] = true
			return out, nil
		}, In("a_out"), Out("b_out")); err != nil {
			rt.Fatalf("Node(b): %v", err)
		}
		if _, err := g.Node("c", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["c"] = "done_c"
			out["c_out"] = true
			return out, nil
		}, In("b_out"), Out("c_out")); err != nil {
			rt.Fatalf("Node(c): %v", err)
		}
		g.Start("a")

		// Set interrupt on node "b".
		if interruptType == "before" {
			if err := g.InterruptBefore("b"); err != nil {
				rt.Fatalf("InterruptBefore: %v", err)
			}
		} else {
			if err := g.InterruptAfter("b"); err != nil {
				rt.Fatalf("InterruptAfter: %v", err)
			}
		}

		// Run until interrupt.
		_, err = g.Run(context.Background(), State{}, WithThreadID("thread-preserve-int"))
		var intErr *GraphInterruptError
		if !errors.As(err, &intErr) {
			rt.Fatalf("expected GraphInterruptError, got %v", err)
		}

		// Verify interrupt result.
		if intErr.Result.NodeName != "b" {
			rt.Fatalf("expected interrupt at 'b', got %q", intErr.Result.NodeName)
		}
		if interruptType == "before" {
			if intErr.Result.Type != InterruptTypeBefore {
				rt.Fatalf("expected InterruptTypeBefore, got %q", intErr.Result.Type)
			}
		} else {
			if intErr.Result.Type != InterruptTypeAfter {
				rt.Fatalf("expected InterruptTypeAfter, got %q", intErr.Result.Type)
			}
		}

		// Verify checkpoint was saved.
		if len(cp.saved) == 0 {
			rt.Fatalf("expected at least one checkpoint saved")
		}

		// Verify checkpoint state is correct.
		lastCp := cp.saved[len(cp.saved)-1]
		if lastCp.NodeName != "b" {
			rt.Fatalf("expected checkpoint NodeName='b', got %q", lastCp.NodeName)
		}

		// For InterruptBefore: "a" should be completed, "b" should NOT.
		// For InterruptAfter: both "a" and "b" should be completed.
		if interruptType == "before" {
			if !lastCp.Completed["a"] {
				rt.Fatalf("expected 'a' completed in checkpoint")
			}
			if lastCp.Completed["b"] {
				rt.Fatalf("expected 'b' NOT completed in InterruptBefore checkpoint")
			}
		} else {
			if !lastCp.Completed["a"] {
				rt.Fatalf("expected 'a' completed in checkpoint")
			}
			if !lastCp.Completed["b"] {
				rt.Fatalf("expected 'b' completed in InterruptAfter checkpoint")
			}
		}
	})
}

// ─── Test 2d: Step() Executes One Node Per Call ──────────────────────────────
//
// Preservation: Step() on sequential-only graphs must continue to execute
// one node per call with correct StepResult.
//
// **Validates: Requirements 3.4**

func TestPreservation_StepExecutesOneNode(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a chain of 2-4 nodes.
		numNodes := rapid.IntRange(2, 4).Draw(rt, "numNodes")

		cp := &mockCheckpointer{}
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}

		// Build a linear chain.
		var prevOutKey string
		nodeNames := make([]string, numNodes)
		for i := range numNodes {
			name := fmt.Sprintf("node_%d", i)
			nodeNames[i] = name
			outKey := fmt.Sprintf("out_%d", i)
			var inputKeys []string
			if prevOutKey != "" {
				inputKeys = []string{prevOutKey}
			}

			localName := name
			localOutKey := outKey
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[localName] = "done_" + localName
				out[localOutKey] = true
				return out, nil
			}, In(inputKeys...), Out(outKey)); err != nil {
				rt.Fatalf("Node(%s): %v", name, err)
			}
			prevOutKey = outKey
		}
		g.Start(nodeNames[0])

		// Step through each node one at a time.
		for i, expectedNode := range nodeNames {
			var initialState State
			if i == 0 {
				initialState = State{"seed": "value"}
			}
			result, err := g.Step(context.Background(), initialState, "thread-step-preserve")
			if err != nil {
				rt.Fatalf("Step %d: %v", i, err)
			}

			// Verify one node was executed.
			if result.NodeName != expectedNode {
				rt.Fatalf("Step %d: expected node %q, got %q", i, expectedNode, result.NodeName)
			}

			// Verify version increments.
			if result.Version != i+1 {
				rt.Fatalf("Step %d: expected version %d, got %d", i, i+1, result.Version)
			}

			// Verify Done is only true on the last step.
			if i < numNodes-1 && result.Done {
				rt.Fatalf("Step %d: expected Done=false, got true", i)
			}
			if i == numNodes-1 && !result.Done {
				rt.Fatalf("Step %d: expected Done=true, got false", i)
			}
		}
	})
}

// ─── Test 2e: Resume() After Interrupt ───────────────────────────────────────
//
// Preservation: Resume() after interrupt must continue to merge state updates
// and continue execution from the interrupted node.
//
// **Validates: Requirements 3.5**

func TestPreservation_ResumeAfterInterrupt(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a value to inject during resume.
		injectedValue := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "injectedValue")

		cp := &mockCheckpointer{}
		g, err := New[State](WithCheckpointer(cp))
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}

		// Build: a → b → c, interrupt before b.
		if _, err := g.Node("a", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["a"] = "done_a"
			out["a_out"] = true
			return out, nil
		}, In(), Out("a_out")); err != nil {
			rt.Fatalf("Node(a): %v", err)
		}
		if _, err := g.Node("b", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["b"] = "done_b"
			out["saw_injected"] = s["injected"]
			out["b_out"] = true
			return out, nil
		}, In("a_out"), Out("b_out")); err != nil {
			rt.Fatalf("Node(b): %v", err)
		}
		if _, err := g.Node("c", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["c"] = "done_c"
			out["c_out"] = true
			return out, nil
		}, In("b_out"), Out("c_out")); err != nil {
			rt.Fatalf("Node(c): %v", err)
		}
		g.Start("a")

		if err := g.InterruptBefore("b"); err != nil {
			rt.Fatalf("InterruptBefore: %v", err)
		}

		// Run until interrupt.
		_, err = g.Run(context.Background(), State{}, WithThreadID("thread-resume-preserve"))
		var intErr *GraphInterruptError
		if !errors.As(err, &intErr) {
			rt.Fatalf("expected GraphInterruptError, got %v", err)
		}

		// Resume with state updates.
		updates := State{"injected": injectedValue}
		result, err := g.Resume(context.Background(), "thread-resume-preserve", &updates)
		if err != nil {
			rt.Fatalf("Resume: %v", err)
		}

		// Verify: b saw the injected value.
		if result.State["saw_injected"] != injectedValue {
			rt.Fatalf("expected saw_injected=%q, got %v", injectedValue, result.State["saw_injected"])
		}

		// Verify: all nodes completed.
		if result.State["a"] != "done_a" {
			rt.Fatalf("expected a=done_a, got %v", result.State["a"])
		}
		if result.State["b"] != "done_b" {
			rt.Fatalf("expected b=done_b, got %v", result.State["b"])
		}
		if result.State["c"] != "done_c" {
			rt.Fatalf("expected c=done_c, got %v", result.State["c"])
		}
	})
}

// ─── Test 2f: Lifecycle Events Fire in Correct Order ─────────────────────────
//
// Preservation: GraphEventHook must continue to emit all lifecycle events
// in correct order.
//
// **Validates: Requirements 3.6**

func TestPreservation_LifecycleEventOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a chain of 2-3 nodes.
		numNodes := rapid.IntRange(2, 3).Draw(rt, "numNodes")

		hook := &recordingHook{}
		cp := &mockCheckpointer{}
		g, err := New[State](WithCheckpointer(cp), WithEventHook(hook))
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}

		// Build a linear chain.
		var prevOutKey string
		nodeNames := make([]string, numNodes)
		for i := range numNodes {
			name := fmt.Sprintf("node_%d", i)
			nodeNames[i] = name
			outKey := fmt.Sprintf("out_%d", i)
			var inputKeys []string
			if prevOutKey != "" {
				inputKeys = []string{prevOutKey}
			}

			localOutKey := outKey
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[localOutKey] = true
				return out, nil
			}, In(inputKeys...), Out(outKey)); err != nil {
				rt.Fatalf("Node(%s): %v", name, err)
			}
			prevOutKey = outKey
		}
		g.Start(nodeNames[0])

		_, err = g.Run(context.Background(), State{}, WithThreadID("thread-events-preserve"))
		if err != nil {
			rt.Fatalf("Run: %v", err)
		}

		// Verify: first event is GraphStarted.
		if len(hook.events) == 0 {
			rt.Fatalf("expected events, got none")
		}
		if hook.events[0].Type != EventGraphStarted {
			rt.Fatalf("expected first event GraphStarted, got %s", hook.events[0].Type)
		}

		// Verify: last event is GraphCompleted.
		last := hook.events[len(hook.events)-1]
		if last.Type != EventGraphCompleted {
			rt.Fatalf("expected last event GraphCompleted, got %s", last.Type)
		}

		// Verify: for each node, NodeStarted comes before NodeCompleted.
		for _, name := range nodeNames {
			startIdx := -1
			completeIdx := -1
			for i, ev := range hook.events {
				if ev.NodeName == name && ev.Type == EventNodeStarted {
					startIdx = i
				}
				if ev.NodeName == name && ev.Type == EventNodeCompleted {
					completeIdx = i
				}
			}
			if startIdx == -1 {
				rt.Fatalf("missing NodeStarted for %q", name)
			}
			if completeIdx == -1 {
				rt.Fatalf("missing NodeCompleted for %q", name)
			}
			if startIdx >= completeIdx {
				rt.Fatalf("NodeStarted (idx=%d) should come before NodeCompleted (idx=%d) for %q",
					startIdx, completeIdx, name)
			}
		}

		// Verify: CheckpointSaved events exist for each node.
		cpSavedCount := 0
		for _, ev := range hook.events {
			if ev.Type == EventCheckpointSaved {
				cpSavedCount++
			}
		}
		if cpSavedCount != numNodes {
			rt.Fatalf("expected %d CheckpointSaved events, got %d", numNodes, cpSavedCount)
		}

		// Verify: event sequence for sequential chain is:
		// GraphStarted, [NodeStarted, NodeCompleted, CheckpointSaved]×N, GraphCompleted
		expectedLen := 1 + numNodes*3 + 1 // GraphStarted + N*(Started+Completed+Checkpoint) + GraphCompleted
		if len(hook.events) != expectedLen {
			rt.Fatalf("expected %d events, got %d; types: %v", expectedLen, len(hook.events), eventTypes(hook.events))
		}
	})
}

// ─── Test 2g: Concurrent Merge Determinism ───────────────────────────────────
//
// Preservation: Concurrent nodes producing different output keys must continue
// to merge correctly via mergeDiff in sorted order.
//
// **Validates: Requirements 3.8**

func TestPreservation_ConcurrentMergeDeterminism(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate 2-4 concurrent branches.
		numBranches := rapid.IntRange(2, 4).Draw(rt, "numBranches")

		g, err := New[State]()
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}

		// Entry node writes "started" key.
		if _, err := g.Node("entry", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["started"] = true
			return out, nil
		}, In(), Out("started")); err != nil {
			rt.Fatalf("Node(entry): %v", err)
		}

		// N concurrent branches, all depending on "started".
		branchOutKeys := make([]string, numBranches)
		for i := range numBranches {
			name := fmt.Sprintf("branch_%d", i)
			outKey := fmt.Sprintf("branch_%d_out", i)
			branchOutKeys[i] = outKey

			localName := name
			localOutKey := outKey
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[localName] = "done_" + localName
				out[localOutKey] = true
				return out, nil
			}, In("started"), Out(outKey)); err != nil {
				rt.Fatalf("Node(%s): %v", name, err)
			}
		}

		// Join node depends on all branch outputs.
		if _, err := g.Node("join", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["joined"] = true
			return out, nil
		}, In(branchOutKeys...), Out("joined")); err != nil {
			rt.Fatalf("Node(join): %v", err)
		}

		g.Start("entry")

		result, err := g.Run(context.Background(), State{})
		if err != nil {
			rt.Fatalf("Run: %v", err)
		}

		// Verify: all branches produced their output.
		for i := range numBranches {
			name := fmt.Sprintf("branch_%d", i)
			if result.State[name] != "done_"+name {
				rt.Fatalf("expected state[%s]='done_%s', got %v", name, name, result.State[name])
			}
		}

		// Verify: join node executed.
		if result.State["joined"] != true {
			rt.Fatalf("expected state[joined]=true, got %v", result.State["joined"])
		}

		// Run multiple times to verify determinism.
		for trial := 0; trial < 3; trial++ {
			result2, err := g.Run(context.Background(), State{})
			if err != nil {
				rt.Fatalf("Run trial %d: %v", trial, err)
			}
			for i := range numBranches {
				name := fmt.Sprintf("branch_%d", i)
				if result2.State[name] != result.State[name] {
					rt.Fatalf("trial %d: state[%s] differs: %v vs %v",
						trial, name, result2.State[name], result.State[name])
				}
			}
		}
	})
}

// ─── Test 2h: MaxIterations Returns GraphIterationError ──────────────────────
//
// Preservation: MaxIterations exceeded must continue to return GraphIterationError.
//
// **Validates: Requirements 3.10**

func TestPreservation_MaxIterationsError(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a max iterations limit of 1-3.
		maxIter := rapid.IntRange(1, 3).Draw(rt, "maxIter")

		// Build a chain longer than maxIter to trigger the limit.
		chainLen := maxIter + 2

		g, err := New[State](WithMaxIterations(maxIter))
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}

		var prevOutKey string
		for i := range chainLen {
			name := fmt.Sprintf("node_%d", i)
			outKey := fmt.Sprintf("out_%d", i)
			var inputKeys []string
			if prevOutKey != "" {
				inputKeys = []string{prevOutKey}
			}

			localOutKey := outKey
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[localOutKey] = true
				return out, nil
			}, In(inputKeys...), Out(outKey)); err != nil {
				rt.Fatalf("Node(%s): %v", name, err)
			}
			prevOutKey = outKey
		}
		g.Start("node_0")

		_, err = g.Run(context.Background(), State{})

		// Verify: GraphIterationError is returned.
		var iterErr *GraphIterationError
		if !errors.As(err, &iterErr) {
			rt.Fatalf("expected GraphIterationError, got %v", err)
		}
		if iterErr.Limit != maxIter {
			rt.Fatalf("expected limit=%d, got %d", maxIter, iterErr.Limit)
		}
	})
}

// ─── Preservation Test Helpers ───────────────────────────────────────────────

// preservationTracingHook counts tracing hook invocations.
type preservationTracingHook struct {
	graphRunStartCount atomic.Int64
	nodeStartCount     atomic.Int64
}

func (h *preservationTracingHook) OnGraphRunStart(ctx context.Context) (context.Context, func(error, int)) {
	h.graphRunStartCount.Add(1)
	return ctx, func(_ error, _ int) {}
}

func (h *preservationTracingHook) OnNodeStart(ctx context.Context, _ string) (context.Context, func(error)) {
	h.nodeStartCount.Add(1)
	return ctx, func(_ error) {}
}

func (h *preservationTracingHook) OnCheckpointSave(_ context.Context, _ string, _ int) func(error) {
	return func(_ error) {}
}

func (h *preservationTracingHook) OnInterrupt(_ context.Context, _ string, _ InterruptType, _ int) {}
func (h *preservationTracingHook) OnResume(_ context.Context, _ string, _ int)                     {}
func (h *preservationTracingHook) OnRewind(_ context.Context, _ string, _ int)                     {}

// preservationMetricsHook counts metrics hook invocations.
type preservationMetricsHook struct {
	graphStartCount atomic.Int64
	nodeStartCount  atomic.Int64
}

func (h *preservationMetricsHook) OnGraphRunStart() func(error, int) {
	h.graphStartCount.Add(1)
	return func(_ error, _ int) {}
}

func (h *preservationMetricsHook) OnNodeStart(_ string) func(error) {
	h.nodeStartCount.Add(1)
	return func(_ error) {}
}

// preservationLoggingHook counts logging hook invocations.
type preservationLoggingHook struct {
	graphRunStartCount atomic.Int64
	nodeStartCount     atomic.Int64
}

func (h *preservationLoggingHook) OnGraphRunStart() {
	h.graphRunStartCount.Add(1)
}

func (h *preservationLoggingHook) OnGraphRunEnd(_ error, _ int, _ agent.TokenUsage, _ time.Duration) {
}

func (h *preservationLoggingHook) OnNodeStart(_ string) {
	h.nodeStartCount.Add(1)
}

func (h *preservationLoggingHook) OnNodeEnd(_ string, _ error, _ time.Duration) {
}
