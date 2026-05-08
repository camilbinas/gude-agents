package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestAgentNode_UsesInvokeStream validates Req 4.1:
// THE Agent_Node SHALL invoke the agent using the streaming path (equivalent to
// InvokeStream) rather than the non-streaming Invoke path.
func TestAgentNode_UsesInvokeStream(t *testing.T) {
	// Create a mock that tracks whether InvokeStream was called.
	invoker := &trackingStreamInvoker{
		response: "streamed response",
	}

	hook := &recordingHook{}
	c := agent.NewContext(context.Background())

	result, err := agentNodeStream(invoker, c, "hello", hook, "test-node")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify InvokeStream was called (not Invoke).
	if !invoker.invokeStreamCalled {
		t.Fatal("expected InvokeStream to be called")
	}

	// Verify the result contains the streamed response.
	if result != "streamed response" {
		t.Errorf("expected result=%q, got %q", "streamed response", result)
	}

	// Verify streaming events were emitted.
	var streamingEvents int
	for _, ev := range hook.events {
		if ev.Type == EventAgentStreaming {
			streamingEvents++
		}
	}
	if streamingEvents == 0 {
		t.Error("expected at least one AgentStreaming event")
	}
}

// TestAgentNode_StreamingFallback validates Req 4.4:
// IF streaming is not supported by the agent's provider, THEN THE Agent_Node SHALL
// fall back to non-streaming invocation and emit the complete response as a single
// chunk event.
func TestAgentNode_StreamingFallback(t *testing.T) {
	// Create a mock that never calls the stream callback (simulating a provider
	// that doesn't support streaming) but provides a fallback response.
	invoker := &nonStreamingInvoker{
		response: "full response without streaming",
	}

	hook := &recordingHook{}
	c := agent.NewContext(context.Background())

	result, err := agentNodeStream(invoker, c, "hello", hook, "fallback-node")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the full response is returned.
	if result != "full response without streaming" {
		t.Errorf("expected result=%q, got %q", "full response without streaming", result)
	}

	// Verify exactly one streaming event was emitted with the full response.
	var streamingEvents []GraphEvent
	for _, ev := range hook.events {
		if ev.Type == EventAgentStreaming {
			streamingEvents = append(streamingEvents, ev)
		}
	}
	if len(streamingEvents) != 1 {
		t.Fatalf("expected 1 AgentStreaming event (fallback), got %d", len(streamingEvents))
	}

	ev := streamingEvents[0]
	if ev.Chunk != "full response without streaming" {
		t.Errorf("expected Chunk=%q, got %q", "full response without streaming", ev.Chunk)
	}
	if ev.NodeName != "fallback-node" {
		t.Errorf("expected NodeName=%q, got %q", "fallback-node", ev.NodeName)
	}
}

func TestAgentNode_StreamingError(t *testing.T) {
	// Verify that errors from InvokeStream are propagated.
	expectedErr := errors.New("provider failure")
	invoker := &errorStreamInvoker{err: expectedErr}

	hook := &recordingHook{}
	c := agent.NewContext(context.Background())

	_, err := agentNodeStream(invoker, c, "hello", hook, "error-node")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}

	// No streaming events should be emitted on error.
	for _, ev := range hook.events {
		if ev.Type == EventAgentStreaming {
			t.Error("unexpected AgentStreaming event on error path")
		}
	}
}

func TestAgentNode_StreamingNilHook(t *testing.T) {
	// Verify that agentNodeStream works correctly when graphHook is nil.
	invoker := &mockStreamInvoker{
		chunks: []string{"hello", " ", "world"},
	}

	c := agent.NewContext(context.Background())

	result, err := agentNodeStream(invoker, c, "test", nil, "no-hook-node")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "hello world" {
		t.Errorf("expected result=%q, got %q", "hello world", result)
	}
}

func TestAgentNode_StreamingEmptyChunksSkipped(t *testing.T) {
	// Verify that empty chunks don't produce events.
	invoker := &mockStreamInvoker{
		chunks: []string{"hello", "", "world", ""},
	}

	hook := &recordingHook{}
	c := agent.NewContext(context.Background())

	result, err := agentNodeStream(invoker, c, "test", hook, "skip-empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result should only contain non-empty chunks.
	if result != "helloworld" {
		t.Errorf("expected result=%q, got %q", "helloworld", result)
	}

	// Only non-empty chunks should produce events.
	var streamingEvents []GraphEvent
	for _, ev := range hook.events {
		if ev.Type == EventAgentStreaming {
			streamingEvents = append(streamingEvents, ev)
		}
	}
	if len(streamingEvents) != 2 {
		t.Fatalf("expected 2 AgentStreaming events (non-empty chunks only), got %d", len(streamingEvents))
	}
	if streamingEvents[0].Chunk != "hello" {
		t.Errorf("expected first chunk=%q, got %q", "hello", streamingEvents[0].Chunk)
	}
	if streamingEvents[1].Chunk != "world" {
		t.Errorf("expected second chunk=%q, got %q", "world", streamingEvents[1].Chunk)
	}
}

// ── test mock implementations ────────────────────────────────────────────────

// trackingStreamInvoker tracks whether InvokeStream was called and streams
// the response word by word.
type trackingStreamInvoker struct {
	invokeStreamCalled bool
	response           string
}

func (t *trackingStreamInvoker) InvokeStream(_ *agent.Context, _ string, cb agent.StreamCallback) error {
	t.invokeStreamCalled = true
	if cb != nil {
		cb(t.response)
	}
	return nil
}

// nonStreamingInvoker simulates a provider that doesn't support streaming.
// It never calls the stream callback but provides the response via FallbackStreamInvoker.
type nonStreamingInvoker struct {
	response string
}

func (n *nonStreamingInvoker) InvokeStream(_ *agent.Context, _ string, _ agent.StreamCallback) error {
	// Deliberately does NOT call the callback — simulates non-streaming provider.
	return nil
}

// LastResponse implements FallbackStreamInvoker.
func (n *nonStreamingInvoker) LastResponse() string {
	return n.response
}

// errorStreamInvoker always returns an error.
type errorStreamInvoker struct {
	err error
}

func (e *errorStreamInvoker) InvokeStream(_ *agent.Context, _ string, _ agent.StreamCallback) error {
	return e.err
}
