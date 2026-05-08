package graph

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"pgregory.net/rapid"
)

// ─── Bug Condition Exploration Tests ─────────────────────────────────────────
//
// These tests encode the EXPECTED behavior after the fix. They are designed to
// FAIL on unfixed code, confirming the bugs exist. When the fix is applied,
// these tests will PASS, confirming the bugs are resolved.
//
// **Validates: Requirements 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 1.9**

// ─── Test 1a: Race Detection ─────────────────────────────────────────────────
//
// Bug Condition: input.hasConcurrentAgentNodes AND input.agentNodesShareSameAgent
//
// When two concurrent agent nodes share the same *agent.Agent, configureBridgeHooks
// mutates the shared agent's hook fields without synchronization, causing data races.
// This test runs under -race and will FAIL on unfixed code with a data race report.
//
// **Validates: Requirements 1.1, 1.2, 1.3**

// TestBugCondition_RaceDetection_ConcurrentAgentNodes runs a graph with 2+
// concurrent agent nodes sharing one *agent.Agent under the -race flag.
// Expected: no data races on hook fields.
// On unfixed code: race detector fires on SetTracingHook/SetMetricsHook/SetLoggingHook.
func TestBugCondition_RaceDetection_ConcurrentAgentNodes(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate 2-4 concurrent agent nodes sharing the same agent.
		numNodes := rapid.IntRange(2, 4).Draw(rt, "numConcurrentAgentNodes")

		// Create a single shared agent with a scripted provider.
		sp := &raceTestProvider{}
		a, err := agent.New(sp, prompt.Text("shared agent"), nil)
		if err != nil {
			rt.Fatalf("agent.New: %v", err)
		}

		// Build a graph: entry → [agent1, agent2, ...agentN] (all concurrent)
		g, err := New[State]()
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}

		// Configure tracing and metrics hooks to trigger the race condition.
		tracingHook := &raceTestTracingHook{}
		metricsHook := &raceTestMetricsHook{}
		g.SetGraphTracingHook(tracingHook)
		g.SetGraphMetricsHook(metricsHook)

		// Entry node writes "started" key.
		if _, err := g.Node("entry", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["started"] = true
			return out, nil
		}, In(), Out("started")); err != nil {
			rt.Fatalf("Node(entry): %v", err)
		}

		// Register N agent nodes, all depending on "started" (concurrent).
		for i := 0; i < numNodes; i++ {
			nodeName := rapid.SampledFrom([]string{"agent_a", "agent_b", "agent_c", "agent_d"}).Draw(rt, "")
			// Use deterministic names to avoid collisions.
			nodeName = nodeName[:len(nodeName)-1] + string(rune('a'+i))
			inputKey := "started"
			outputKey := nodeName + "_out"

			if _, err := g.Agent(nodeName, a, AgentNodeAccessor[State]{
				GetInput:   func(s State) string { return "test input" },
				SetOutput:  func(s *State, out string) { (*s)[outputKey] = out },
				InputKeys:  []string{inputKey},
				OutputKeys: []string{outputKey},
			}); err != nil {
				rt.Fatalf("Agent(%s): %v", nodeName, err)
			}
		}

		g.Start("entry")

		// Run the graph. Under -race, this will detect data races on hook fields
		// if configureBridgeHooks mutates the shared agent concurrently.
		_, err = g.Run(context.Background(), State{})
		if err != nil {
			// Errors from the provider are acceptable — we're testing for races, not correctness.
			// But the race detector will still fire independently of this error.
			_ = err
		}
	})
}

// ─── Test 1b: Concurrent Checkpoint Count ────────────────────────────────────
//
// Bug Condition: input.hasConcurrentNodes AND input.checkpointerConfigured
//
// When executeConcurrent completes N nodes, it saves only ONE checkpoint with
// NodeName = nodes[len(nodes)-1]. Expected: N checkpoints (one per node).
//
// **Validates: Requirements 1.4, 1.5**

// TestBugCondition_ConcurrentCheckpointCount runs a graph with 3 concurrent
// nodes and a checkpointer, asserting checkpoint save count equals node count.
// On unfixed code: checkpoint count is 1 instead of 3.
func TestBugCondition_ConcurrentCheckpointCount(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate 2-5 concurrent nodes.
		numConcurrent := rapid.IntRange(3, 5).Draw(rt, "numConcurrentNodes")

		cp := &mockCheckpointer{}
		g := mustGraphForBugfix(rt, WithCheckpointer(cp))

		// Entry node writes "trigger" key.
		if _, err := g.Node("entry", func(_ context.Context, s State) (State, error) {
			out := CopyState(s)
			out["trigger"] = true
			return out, nil
		}, In(), Out("trigger")); err != nil {
			rt.Fatalf("Node(entry): %v", err)
		}

		// Register N concurrent nodes, all depending on "trigger".
		for i := 0; i < numConcurrent; i++ {
			name := string(rune('a'+i)) + "_node"
			outKey := name + "_out"
			if _, err := g.Node(name, func(_ context.Context, s State) (State, error) {
				out := CopyState(s)
				out[outKey] = true
				return out, nil
			}, In("trigger"), Out(outKey)); err != nil {
				rt.Fatalf("Node(%s): %v", name, err)
			}
		}

		g.Start("entry")

		_, err := g.Run(context.Background(), State{}, WithThreadID("test-thread"))
		if err != nil {
			rt.Fatalf("Run: %v", err)
		}

		// Expected: 1 checkpoint for entry + N checkpoints for concurrent nodes = N+1 total.
		// Bug: only 1 checkpoint for entry + 1 checkpoint for the batch = 2 total.
		expectedCheckpoints := 1 + numConcurrent // entry + one per concurrent node
		if len(cp.saved) != expectedCheckpoints {
			rt.Fatalf("expected %d checkpoints (1 entry + %d concurrent), got %d",
				expectedCheckpoints, numConcurrent, len(cp.saved))
		}
	})
}

// ─── Test 1c: Double Metrics ─────────────────────────────────────────────────
//
// Bug Condition: input.hasAgentNode AND input.graphMetricsHookConfigured
//
// When bridgeMetricsHook.OnInvokeStart is called for an agent node, it calls
// graphHook.OnNodeStart(nodeName) in addition to the engine's own call.
// This results in OnNodeStart being called TWICE per agent node.
//
// **Validates: Requirements 1.6, 1.7**

// TestBugCondition_DoubleMetrics configures a counting GraphMetricsHook, runs
// a single agent node, and asserts OnNodeStart is called exactly once per node.
// On unfixed code: OnNodeStart count is 2 instead of 1 for the agent node.
func TestBugCondition_DoubleMetrics(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Create a single agent node graph with a counting metrics hook.
		sp := &raceTestProvider{}
		a, err := agent.New(sp, prompt.Text("metrics test agent"), nil)
		if err != nil {
			rt.Fatalf("agent.New: %v", err)
		}

		countingHook := &countingMetricsHook{}
		g, err := New[State]()
		if err != nil {
			rt.Fatalf("New[State]: %v", err)
		}
		g.SetGraphMetricsHook(countingHook)

		// Register a single agent node as the entry.
		if _, err := g.Agent("my_agent", a, AgentNodeAccessor[State]{
			GetInput:   func(s State) string { return s["input"].(string) },
			SetOutput:  func(s *State, out string) { (*s)["output"] = out },
			InputKeys:  []string{"input"},
			OutputKeys: []string{"output"},
		}); err != nil {
			rt.Fatalf("Agent: %v", err)
		}
		g.Start("my_agent")

		_, err = g.Run(context.Background(), State{"input": "hello"})
		if err != nil {
			rt.Fatalf("Run: %v", err)
		}

		// Expected: OnNodeStart called exactly 1 time for "my_agent".
		// Bug: OnNodeStart called 2 times (once from engine, once from bridgeMetricsHook).
		nodeStartCount := countingHook.nodeStartCount.Load()
		if nodeStartCount != 1 {
			rt.Fatalf("expected OnNodeStart called 1 time, got %d (double-counting bug)", nodeStartCount)
		}
	})
}

// ─── Test 1d: Typed State Marshal ────────────────────────────────────────────
//
// Bug Condition: input.usesTypedState AND input.nodeCompleted
//
// When a typed-state graph completes a node, updateReadiness calls
// e.ops.toMap(e.state) which performs a full json.Marshal of the entire state.
// This is unnecessary overhead when no event hook or checkpointer is configured.
//
// **Validates: Requirements 1.8, 1.9**

// marshalCountingState is a typed state that tracks json.Marshal calls.
type marshalCountingState struct {
	Input  string `json:"input"`
	Middle string `json:"middle"`
	Output string `json:"output"`
}

// TestBugCondition_TypedStateMarshal runs a typed-state graph with no event hook
// or checkpointer and asserts that updateReadiness does NOT perform full json.Marshal.
// On unfixed code: json.Marshal is called in updateReadiness after every node completion.
func TestBugCondition_TypedStateMarshal(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Track json.Marshal calls by wrapping the state operations.
		// We use a typed-state graph with NO event hook and NO checkpointer.
		// The only place json.Marshal should be called is in ops.copy (for state isolation).
		// updateReadiness should NOT call ops.toMap (which triggers json.Marshal).

		g, err := New[marshalCountingState]()
		if err != nil {
			rt.Fatalf("New[marshalCountingState]: %v", err)
		}

		// Linear chain: entry → process → finish
		_, err = g.Node("entry", func(_ context.Context, s marshalCountingState) (marshalCountingState, error) {
			s.Input = "hello"
			return s, nil
		}, In(), Out("input"))
		if err != nil {
			rt.Fatalf("Node(entry): %v", err)
		}

		_, err = g.Node("process", func(_ context.Context, s marshalCountingState) (marshalCountingState, error) {
			s.Middle = s.Input + "_processed"
			return s, nil
		}, In("input"), Out("middle"))
		if err != nil {
			rt.Fatalf("Node(process): %v", err)
		}

		_, err = g.Node("finish", func(_ context.Context, s marshalCountingState) (marshalCountingState, error) {
			s.Output = s.Middle + "_done"
			return s, nil
		}, In("middle"), Out("output"))
		if err != nil {
			rt.Fatalf("Node(finish): %v", err)
		}

		g.Start("entry")

		// Count json.Marshal calls during execution by instrumenting the state ops.
		// We'll measure by checking if updateReadiness performs toMap calls.
		// Since we can't easily instrument json.Marshal globally, we verify the behavior
		// by checking that the ops.toMap is NOT called in updateReadiness when no
		// event hook or checkpointer is configured.
		//
		// Strategy: Replace the graph's ops with a counting wrapper.
		marshalCount := &atomic.Int64{}
		g.ops = &countingJsonStateOps[marshalCountingState]{
			inner:        jsonStateOps[marshalCountingState]{},
			toMapCounter: marshalCount,
		}

		result, err := g.Run(context.Background(), marshalCountingState{})
		if err != nil {
			rt.Fatalf("Run: %v", err)
		}

		// Verify the graph executed correctly.
		if result.State.Output != "hello_processed_done" {
			rt.Fatalf("expected Output='hello_processed_done', got %q", result.State.Output)
		}

		// Count how many toMap calls occurred.
		// Expected (after fix): toMap should only be called during Run's initial key extraction (1 call).
		// Bug (unfixed): toMap is called in updateReadiness after EACH node completion (3 additional calls).
		//
		// Breakdown of expected toMap calls:
		// - 1 call in Run() for initial state key extraction
		// - 0 calls in updateReadiness (after fix)
		// Total expected: 1
		//
		// Breakdown of buggy toMap calls:
		// - 1 call in Run() for initial state key extraction
		// - 3 calls in updateReadiness (one per node: entry, process, finish)
		// Total buggy: 4
		totalToMapCalls := marshalCount.Load()
		if totalToMapCalls > 1 {
			rt.Fatalf("expected at most 1 toMap call (initial key extraction), got %d "+
				"(updateReadiness is performing unnecessary json.Marshal)", totalToMapCalls)
		}
	})
}

// ─── Test Helpers ────────────────────────────────────────────────────────────

// raceTestProvider is a minimal provider that returns a fixed response.
// Safe for concurrent use.
type raceTestProvider struct{}

func (p *raceTestProvider) Name() string { return "mock" }

func (p *raceTestProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	return &agent.ProviderResponse{Text: "mock response"}, nil
}

func (p *raceTestProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	if cb != nil {
		cb("mock response")
	}
	return &agent.ProviderResponse{Text: "mock response"}, nil
}

// raceTestTracingHook is a minimal tracing hook for triggering bridge hook configuration.
type raceTestTracingHook struct{}

func (h *raceTestTracingHook) OnGraphRunStart(ctx context.Context) (context.Context, func(error, int)) {
	return ctx, func(_ error, _ int) {}
}
func (h *raceTestTracingHook) OnNodeStart(ctx context.Context, _ string) (context.Context, func(error)) {
	return ctx, func(_ error) {}
}
func (h *raceTestTracingHook) OnCheckpointSave(_ context.Context, _ string, _ int) func(error) {
	return func(_ error) {}
}
func (h *raceTestTracingHook) OnInterrupt(_ context.Context, _ string, _ InterruptType, _ int) {}
func (h *raceTestTracingHook) OnResume(_ context.Context, _ string, _ int)                     {}
func (h *raceTestTracingHook) OnRewind(_ context.Context, _ string, _ int)                     {}

// raceTestMetricsHook is a minimal metrics hook for triggering bridge hook configuration.
type raceTestMetricsHook struct{}

func (h *raceTestMetricsHook) OnGraphRunStart() func(error, int) {
	return func(_ error, _ int) {}
}
func (h *raceTestMetricsHook) OnNodeStart(_ string) func(error) {
	return func(_ error) {}
}

// countingMetricsHook counts OnNodeStart calls to detect double-counting.
type countingMetricsHook struct {
	nodeStartCount  atomic.Int64
	graphStartCount atomic.Int64
}

func (h *countingMetricsHook) OnGraphRunStart() func(error, int) {
	h.graphStartCount.Add(1)
	return func(_ error, _ int) {}
}

func (h *countingMetricsHook) OnNodeStart(_ string) func(error) {
	h.nodeStartCount.Add(1)
	return func(_ error) {}
}

// countingJsonStateOps wraps jsonStateOps to count toMap calls.
type countingJsonStateOps[S any] struct {
	inner        jsonStateOps[S]
	toMapCounter *atomic.Int64
}

func (c *countingJsonStateOps[S]) copy(s S) S             { return c.inner.copy(s) }
func (c *countingJsonStateOps[S]) merge(base *S, patch S) { c.inner.merge(base, patch) }
func (c *countingJsonStateOps[S]) mergeDiff(base *S, snapshot, branch S) {
	c.inner.mergeDiff(base, snapshot, branch)
}
func (c *countingJsonStateOps[S]) toMap(s S) (map[string]any, error) {
	c.toMapCounter.Add(1)
	return c.inner.toMap(s)
}
func (c *countingJsonStateOps[S]) fromMap(m map[string]any) (S, error) { return c.inner.fromMap(m) }
func (c *countingJsonStateOps[S]) hasKey(state S, key string) bool     { return c.inner.hasKey(state, key) }

// mustGraphForBugfix creates a graph for bugfix tests using rapid.T for fatal errors.
func mustGraphForBugfix(rt *rapid.T, opts ...GraphOption) *Graph[State] {
	g, err := New[State](opts...)
	if err != nil {
		rt.Fatalf("New[State]: %v", err)
	}
	return g
}

// Ensure json import is used (for marshalCountingState).
var _ = json.Marshal
