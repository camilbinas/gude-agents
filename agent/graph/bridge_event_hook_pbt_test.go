package graph

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// Feature: graph-agent-node-integration, Property 2: Tool call start event completeness
//
// For any agent node execution where the agent invokes a tool with a given name
// and JSON input, the graph SHALL emit an AgentToolCallStart GraphEvent where
// ToolName equals the tool name, ToolInput equals the input JSON, and NodeName
// equals the registration name of the agent node.
//
// **Validates: Requirements 2.1, 5.1**

func TestProperty_ToolCallStartEvent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random tool name and node name.
		toolName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "toolName")
		nodeName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "nodeName")

		// Generate random JSON input.
		inputMap := map[string]any{
			"key": rapid.String().Draw(rt, "inputValue"),
		}
		inputJSON, err := json.Marshal(inputMap)
		if err != nil {
			rt.Fatalf("failed to marshal input: %v", err)
		}

		// Set up a recording graph event hook.
		hook := &recordingHook{}

		// Create the bridge event hook with no inner hook.
		bridge := newBridgeEventHook(hook, nodeName, nil)
		if bridge == nil {
			rt.Fatal("bridge should not be nil when graphHook is non-nil")
		}

		// Simulate OnToolCallStart call.
		c := agent.NewContext(context.Background())
		bridge.OnToolCallStart(c, toolName, json.RawMessage(inputJSON))

		// Verify exactly one event was emitted.
		if len(hook.events) != 1 {
			rt.Fatalf("expected 1 event, got %d", len(hook.events))
		}

		ev := hook.events[0]

		// Verify event type.
		if ev.Type != EventAgentToolCallStart {
			rt.Fatalf("expected event type %s, got %s", EventAgentToolCallStart, ev.Type)
		}

		// Verify tool name.
		if ev.ToolName != toolName {
			rt.Fatalf("expected ToolName=%q, got %q", toolName, ev.ToolName)
		}

		// Verify tool input.
		if string(ev.ToolInput) != string(inputJSON) {
			rt.Fatalf("expected ToolInput=%s, got %s", string(inputJSON), string(ev.ToolInput))
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

// Feature: graph-agent-node-integration, Property 3: Tool call end event completeness
//
// For any agent node execution where a tool call completes (successfully or with error),
// the graph SHALL emit an AgentToolCallEnd GraphEvent where ToolName equals the tool name,
// ToolOutput equals the output string (or empty on error), Error contains the error
// (or nil on success), ToolDuration is positive, and NodeName equals the registration name.
//
// **Validates: Requirements 2.2, 5.2, 5.3**

func TestProperty_ToolCallEndEvent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random tool name and node name.
		toolName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "toolName")
		nodeName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "nodeName")

		// Decide if this is a success or error case.
		isError := rapid.Bool().Draw(rt, "isError")

		var output string
		var toolErr error
		if isError {
			errMsg := rapid.StringMatching(`[a-zA-Z ]{1,30}`).Draw(rt, "errorMsg")
			toolErr = errors.New(errMsg)
			output = "" // empty on error per spec
		} else {
			output = rapid.String().Draw(rt, "output")
			toolErr = nil
		}

		// Generate a positive duration.
		durationMs := rapid.Int64Range(1, 10000).Draw(rt, "durationMs")
		duration := time.Duration(durationMs) * time.Millisecond

		// Set up a recording graph event hook.
		hook := &recordingHook{}

		// Create the bridge event hook.
		bridge := newBridgeEventHook(hook, nodeName, nil)
		if bridge == nil {
			rt.Fatal("bridge should not be nil when graphHook is non-nil")
		}

		// Simulate OnToolCallEnd call.
		c := agent.NewContext(context.Background())
		bridge.OnToolCallEnd(c, toolName, output, toolErr, duration)

		// Verify exactly one event was emitted.
		if len(hook.events) != 1 {
			rt.Fatalf("expected 1 event, got %d", len(hook.events))
		}

		ev := hook.events[0]

		// Verify event type.
		if ev.Type != EventAgentToolCallEnd {
			rt.Fatalf("expected event type %s, got %s", EventAgentToolCallEnd, ev.Type)
		}

		// Verify tool name.
		if ev.ToolName != toolName {
			rt.Fatalf("expected ToolName=%q, got %q", toolName, ev.ToolName)
		}

		// Verify tool output.
		if ev.ToolOutput != output {
			rt.Fatalf("expected ToolOutput=%q, got %q", output, ev.ToolOutput)
		}

		// Verify error.
		if isError {
			if ev.Error == nil {
				rt.Fatal("expected non-nil Error on error case")
			}
			if ev.Error.Error() != toolErr.Error() {
				rt.Fatalf("expected Error=%q, got %q", toolErr.Error(), ev.Error.Error())
			}
		} else {
			if ev.Error != nil {
				rt.Fatalf("expected nil Error on success case, got %v", ev.Error)
			}
		}

		// Verify duration is positive.
		if ev.ToolDuration <= 0 {
			rt.Fatalf("expected positive ToolDuration, got %v", ev.ToolDuration)
		}
		if ev.ToolDuration != duration {
			rt.Fatalf("expected ToolDuration=%v, got %v", duration, ev.ToolDuration)
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

// Feature: graph-agent-node-integration, Property 5: Thinking chunk forwarding
//
// For any non-empty thinking chunk string emitted by the agent's provider, the graph
// SHALL emit an AgentThinking GraphEvent where Chunk equals the thinking chunk content
// and NodeName equals the registration name.
//
// **Validates: Requirements 2.5**

func TestProperty_ThinkingChunkForwarding(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random non-empty chunk and node name.
		chunk := rapid.StringMatching(`.{1,100}`).Draw(rt, "chunk")
		nodeName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "nodeName")

		// Set up a recording graph event hook.
		hook := &recordingHook{}

		// Create the bridge event hook.
		bridge := newBridgeEventHook(hook, nodeName, nil)
		if bridge == nil {
			rt.Fatal("bridge should not be nil when graphHook is non-nil")
		}

		// Simulate OnThinking call.
		c := agent.NewContext(context.Background())
		bridge.OnThinking(c, chunk)

		// Verify exactly one event was emitted.
		if len(hook.events) != 1 {
			rt.Fatalf("expected 1 event, got %d", len(hook.events))
		}

		ev := hook.events[0]

		// Verify event type.
		if ev.Type != EventAgentThinking {
			rt.Fatalf("expected event type %s, got %s", EventAgentThinking, ev.Type)
		}

		// Verify chunk content.
		if ev.Chunk != chunk {
			rt.Fatalf("expected Chunk=%q, got %q", chunk, ev.Chunk)
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
