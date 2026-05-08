package graph

import (
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// StreamInvoker abstracts the streaming invocation of an agent.
// *agent.Agent satisfies this interface via its InvokeStream method.
// This interface enables testing agentNodeStream in isolation without a full agent.
type StreamInvoker interface {
	InvokeStream(c *agent.Context, userMessage string, cb agent.StreamCallback) error
}

// compile-time check: *agent.Agent implements StreamInvoker.
var _ StreamInvoker = (*agent.Agent)(nil)

// FallbackStreamInvoker extends StreamInvoker with the ability to report the
// full response text even when the stream callback is never called. This supports
// providers that don't invoke the StreamCallback but still produce a text response.
type FallbackStreamInvoker interface {
	StreamInvoker
	// LastResponse returns the full response text from the most recent InvokeStream call.
	LastResponse() string
}

// agentNodeStream invokes the agent using the streaming path and emits
// EventAgentStreaming GraphEvents for each non-empty chunk received.
// If the provider never calls the stream callback (no streaming support),
// it falls back to emitting a single chunk event with the full response text.
//
// Parameters:
//   - invoker: the streaming agent (typically *agent.Agent)
//   - c: the agent context for the invocation
//   - input: the user message to send to the agent
//   - graphHook: the graph event hook to emit streaming events to (may be nil)
//   - nodeName: the registration name of the agent node (used in events)
//
// Returns the accumulated full response string and any error from the agent.
func agentNodeStream(invoker StreamInvoker, c *agent.Context, input string, graphHook GraphEventHook, nodeName string) (string, error) {
	var sb strings.Builder
	var callbackCalled bool

	// Stream callback: emits EventAgentStreaming for each non-empty chunk
	// and accumulates chunks into the full response.
	cb := func(chunk string) {
		callbackCalled = true
		if chunk == "" {
			return
		}
		sb.WriteString(chunk)
		if graphHook != nil {
			graphHook.OnEvent(GraphEvent{
				Type:      EventAgentStreaming,
				Timestamp: time.Now(),
				NodeName:  nodeName,
				Chunk:     chunk,
			})
		}
	}

	err := invoker.InvokeStream(c, input, cb)
	if err != nil {
		return "", err
	}

	fullResponse := sb.String()

	// Fallback: if the stream callback was never called (provider doesn't support
	// streaming), attempt to get the full response from a FallbackStreamInvoker
	// and emit it as a single chunk event.
	if !callbackCalled {
		if fi, ok := invoker.(FallbackStreamInvoker); ok {
			fallbackText := fi.LastResponse()
			if fallbackText != "" {
				fullResponse = fallbackText
				if graphHook != nil {
					graphHook.OnEvent(GraphEvent{
						Type:      EventAgentStreaming,
						Timestamp: time.Now(),
						NodeName:  nodeName,
						Chunk:     fallbackText,
					})
				}
			}
		}
	}

	return fullResponse, nil
}
