package graph

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestAgentNode_NoEventHook_ZeroOverhead validates Req 2.6:
// IF no Graph_Event_Hook is configured on the Graph, THEN THE Agent_Node SHALL skip
// event emission with zero additional overhead.
//
// This test verifies that when no event hook is configured on the graph, the bridge
// event hook is nil (not created), ensuring zero overhead.
func TestAgentNode_NoEventHook_ZeroOverhead(t *testing.T) {
	t.Run("bridge event hook is nil when graph has no event hook", func(t *testing.T) {
		// newBridgeEventHook should return nil when graphHook is nil.
		bridge := newBridgeEventHook(nil, "test-node", nil)
		if bridge != nil {
			t.Error("expected nil bridge event hook when graph event hook is nil")
		}
	})

	t.Run("bridge event hook is nil with inner hook when graph has no event hook", func(t *testing.T) {
		// Even with an inner hook, bridge should be nil when graph hook is nil.
		inner := &noopEventHook{}
		bridge := newBridgeEventHook(nil, "test-node", inner)
		if bridge != nil {
			t.Error("expected nil bridge event hook when graph event hook is nil, even with inner hook")
		}
	})

	t.Run("graph without event hook does not create bridge in AddAgentNode", func(t *testing.T) {
		// Create a graph WITHOUT an event hook.
		g, err := New[State]()
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		// Verify the graph's event hook is nil.
		if g.eventHook != nil {
			t.Fatal("expected graph event hook to be nil")
		}

		// The bridge factory returns nil for nil graph hook — this is the zero overhead guarantee.
		bridge := newBridgeEventHook(g.eventHook, "test-node", nil)
		if bridge != nil {
			t.Error("expected nil bridge when graph has no event hook configured")
		}
	})

	t.Run("all bridge hooks are nil when graph has no hooks", func(t *testing.T) {
		// Verify zero overhead for all hook types.
		g, err := New[State]()
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		eventBridge := newBridgeEventHook(g.eventHook, "node", nil)
		tracingBridge := newBridgeTracingHook(g.tracingHook, "node", nil)
		metricsBridge := newBridgeMetricsHook(g.metricsHook, "node", nil)
		loggingBridge := newBridgeLoggingHook(g.loggingHook, "node", nil)

		if eventBridge != nil {
			t.Error("expected nil event bridge")
		}
		if tracingBridge != nil {
			t.Error("expected nil tracing bridge")
		}
		if metricsBridge != nil {
			t.Error("expected nil metrics bridge")
		}
		if loggingBridge != nil {
			t.Error("expected nil logging bridge")
		}
	})
}

// noopEventHook is a no-op implementation of agent.EventHook for testing.
type noopEventHook struct {
	agent.BaseEventHook
}
