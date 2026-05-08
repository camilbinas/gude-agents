package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// Feature: graph-agent-node-integration, Property 6: Streaming chunk forwarding
//
// For any non-empty streaming text chunk produced by the agent's provider,
// the graph SHALL emit an AgentStreaming GraphEvent where Chunk equals the
// chunk content and NodeName equals the registration name.
//
// **Validates: Requirements 4.2**

func TestProperty_StreamingChunkForwarding(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random non-empty chunk and node name.
		chunk := rapid.StringMatching(`.{1,100}`).Draw(rt, "chunk")
		nodeName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "nodeName")

		// Create a mock invoker that streams the single chunk via the callback.
		invoker := &mockStreamInvoker{
			chunks: []string{chunk},
		}

		// Set up a recording graph event hook.
		hook := &recordingHook{}

		// Call agentNodeStream.
		c := agent.NewContext(context.Background())
		_, err := agentNodeStream(invoker, c, "test input", hook, nodeName)
		if err != nil {
			rt.Fatalf("unexpected error: %v", err)
		}

		// Verify at least one event was emitted for the chunk.
		found := false
		for _, ev := range hook.events {
			if ev.Type == EventAgentStreaming && ev.Chunk == chunk && ev.NodeName == nodeName {
				found = true
				break
			}
		}
		if !found {
			rt.Fatalf("expected AgentStreaming event with Chunk=%q and NodeName=%q, got events: %v",
				chunk, nodeName, hook.events)
		}
	})
}

// Feature: graph-agent-node-integration, Property 7: Streaming output concatenation
//
// For any sequence of streaming text chunks produced during an agent node execution,
// the value stored in the output state key SHALL equal the concatenation of all
// chunks in the order they were received.
//
// **Validates: Requirements 4.3**

func TestProperty_StreamingOutputConcatenation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random slice of 1-20 non-empty strings.
		numChunks := rapid.IntRange(1, 20).Draw(rt, "numChunks")
		chunks := make([]string, numChunks)
		for i := range chunks {
			chunks[i] = rapid.StringMatching(`.{1,50}`).Draw(rt, "chunk")
		}
		nodeName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "nodeName")

		// Create a mock invoker that streams all chunks via the callback.
		invoker := &mockStreamInvoker{
			chunks: chunks,
		}

		// Set up a recording graph event hook.
		hook := &recordingHook{}

		// Call agentNodeStream.
		c := agent.NewContext(context.Background())
		result, err := agentNodeStream(invoker, c, "test input", hook, nodeName)
		if err != nil {
			rt.Fatalf("unexpected error: %v", err)
		}

		// The result should be the concatenation of all chunks in order.
		expected := strings.Join(chunks, "")
		if result != expected {
			rt.Fatalf("expected result=%q, got %q", expected, result)
		}
	})
}

// ── mock stream invoker for property tests ───────────────────────────────────

// mockStreamInvoker implements StreamInvoker for testing.
// It calls the stream callback with each chunk in order.
type mockStreamInvoker struct {
	chunks []string
}

func (m *mockStreamInvoker) InvokeStream(_ *agent.Context, _ string, cb agent.StreamCallback) error {
	if cb != nil {
		for _, chunk := range m.chunks {
			cb(chunk)
		}
	}
	return nil
}
