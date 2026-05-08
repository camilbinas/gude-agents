package graph

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// Feature: graph-agent-node-integration, Property 4: Model end event includes stop reason
//
// For any agent node execution where the model completes with a stop reason (one of
// "end_turn", "tool_use", "error"), the graph SHALL emit an AgentModelEnd GraphEvent
// where StopReason equals the actual stop reason and NodeName equals the registration name.
//
// **Validates: Requirements 2.4**

func TestProperty_ModelEndStopReason(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random node name.
		nodeName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "nodeName")

		// Draw a random stop reason from the valid set.
		stopReason := rapid.SampledFrom([]string{"end_turn", "tool_use", "error"}).Draw(rt, "stopReason")

		// Set up a recording graph event hook.
		hook := &recordingHook{}

		// Create the bridge event hook with no inner hook.
		bridge := newBridgeEventHook(hook, nodeName, nil)
		if bridge == nil {
			rt.Fatal("bridge should not be nil when graphHook is non-nil")
		}

		// Simulate OnModelEnd call with the stop reason.
		c := agent.NewContext(context.Background())
		bridge.OnModelEnd(c, stopReason)

		// Verify exactly one event was emitted.
		if len(hook.events) != 1 {
			rt.Fatalf("expected 1 event, got %d", len(hook.events))
		}

		ev := hook.events[0]

		// Verify event type.
		if ev.Type != EventAgentModelEnd {
			rt.Fatalf("expected event type %s, got %s", EventAgentModelEnd, ev.Type)
		}

		// Verify stop reason.
		if ev.StopReason != stopReason {
			rt.Fatalf("expected StopReason=%q, got %q", stopReason, ev.StopReason)
		}

		// Verify node name.
		if ev.NodeName != nodeName {
			rt.Fatalf("expected NodeName=%q, got %q", nodeName, ev.NodeName)
		}

		// Verify timestamp is non-zero.
		if ev.Timestamp.IsZero() {
			rt.Fatal("expected non-zero timestamp")
		}
	})
}

// Feature: graph-agent-node-integration, Property 8: Event chronological ordering
//
// For any agent node execution that produces multiple events, the sequence of emitted
// GraphEvents SHALL have monotonically non-decreasing Timestamp values.
//
// **Validates: Requirements 5.4**

func TestProperty_EventChronologicalOrdering(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random node name.
		nodeName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "nodeName")

		// Set up a recording graph event hook.
		hook := &recordingHook{}

		// Create the bridge event hook with no inner hook.
		bridge := newBridgeEventHook(hook, nodeName, nil)
		if bridge == nil {
			rt.Fatal("bridge should not be nil when graphHook is non-nil")
		}

		c := agent.NewContext(context.Background())

		// Generate a random sequence of event method calls (1-10 calls).
		numCalls := rapid.IntRange(2, 10).Draw(rt, "numCalls")
		for i := 0; i < numCalls; i++ {
			method := rapid.IntRange(0, 4).Draw(rt, "method")
			switch method {
			case 0:
				bridge.OnModelStart(c)
			case 1:
				toolName := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "toolName")
				bridge.OnToolCallStart(c, toolName, []byte(`{"key":"value"}`))
			case 2:
				toolName := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "toolName")
				bridge.OnToolCallEnd(c, toolName, "output", nil, 100)
			case 3:
				stopReason := rapid.SampledFrom([]string{"end_turn", "tool_use", "error"}).Draw(rt, "stopReason")
				bridge.OnModelEnd(c, stopReason)
			case 4:
				chunk := rapid.StringMatching(`.{1,20}`).Draw(rt, "chunk")
				bridge.OnThinking(c, chunk)
			}
		}

		// Verify all emitted events have monotonically non-decreasing timestamps.
		if len(hook.events) < 2 {
			rt.Fatalf("expected at least 2 events, got %d", len(hook.events))
		}

		for i := 1; i < len(hook.events); i++ {
			if hook.events[i].Timestamp.Before(hook.events[i-1].Timestamp) {
				rt.Fatalf("event %d timestamp %v is before event %d timestamp %v (types: %s, %s)",
					i, hook.events[i].Timestamp,
					i-1, hook.events[i-1].Timestamp,
					hook.events[i].Type, hook.events[i-1].Type)
			}
		}
	})
}
