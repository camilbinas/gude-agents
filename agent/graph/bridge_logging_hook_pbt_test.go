package graph

import (
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// Feature: graph-agent-node-integration, Property 11: Logging includes node name
//
// For any agent node with a random registration name executing under a graph with a
// GraphLoggingHook, all log entries produced by the bridge logging hook SHALL include
// the node's registration name as context.
//
// **Validates: Requirements 3.3**

func TestProperty_LoggingNodeName(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random node name.
		nodeName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "nodeName")

		// Set up a recording graph logging hook.
		graphHook := &recordingLoggingHook{}

		// Create the bridge logging hook with no inner hook.
		bridge := newBridgeLoggingHook(graphHook, nodeName, nil)
		if bridge == nil {
			rt.Fatal("bridge should not be nil when graphHook is non-nil")
		}

		// Verify the node name is accessible from the bridge.
		if bridge.NodeName() != nodeName {
			rt.Fatalf("expected NodeName()=%q, got %q", nodeName, bridge.NodeName())
		}

		// Simulate OnInvokeStart — should call graphHook.OnNodeStart with the node name.
		bridge.OnInvokeStart(agent.InvokeSpanParams{})

		// Verify the graph logging hook received the node name.
		if len(graphHook.nodeStartCalls) != 1 {
			rt.Fatalf("expected 1 OnNodeStart call, got %d", len(graphHook.nodeStartCalls))
		}
		if graphHook.nodeStartCalls[0] != nodeName {
			rt.Fatalf("expected OnNodeStart called with %q, got %q", nodeName, graphHook.nodeStartCalls[0])
		}

		// Simulate OnInvokeEnd — should call graphHook.OnNodeEnd with the node name.
		bridge.OnInvokeEnd(nil, agent.TokenUsage{}, time.Millisecond)

		if len(graphHook.nodeEndCalls) != 1 {
			rt.Fatalf("expected 1 OnNodeEnd call, got %d", len(graphHook.nodeEndCalls))
		}
		if graphHook.nodeEndCalls[0].nodeName != nodeName {
			rt.Fatalf("expected OnNodeEnd called with %q, got %q", nodeName, graphHook.nodeEndCalls[0].nodeName)
		}
	})
}

// ── test helpers ─────────────────────────────────────────────────────────────

type nodeEndCall struct {
	nodeName string
	err      error
	duration time.Duration
}

type recordingLoggingHook struct {
	graphRunStartCalls int
	graphRunEndCalls   int
	nodeStartCalls     []string
	nodeEndCalls       []nodeEndCall
}

func (h *recordingLoggingHook) OnGraphRunStart() {
	h.graphRunStartCalls++
}

func (h *recordingLoggingHook) OnGraphRunEnd(_ error, _ int, _ agent.TokenUsage, _ time.Duration) {
	h.graphRunEndCalls++
}

func (h *recordingLoggingHook) OnNodeStart(nodeName string) {
	h.nodeStartCalls = append(h.nodeStartCalls, nodeName)
}

func (h *recordingLoggingHook) OnNodeEnd(nodeName string, err error, duration time.Duration) {
	h.nodeEndCalls = append(h.nodeEndCalls, nodeEndCall{nodeName: nodeName, err: err, duration: duration})
}
