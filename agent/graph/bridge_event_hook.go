package graph

import (
	"encoding/json"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// bridgeEventHook implements agent.EventHook and forwards events to GraphEventHook.
// It bridges the agent-level event system to the graph-level event system, enriching
// each event with the originating node name.
//
// When inner is non-nil, both the inner hook and the graph hook are called (composition).
// When graphHook is nil, the bridge should not be created at all (zero overhead).
type bridgeEventHook struct {
	graphHook GraphEventHook
	nodeName  string
	inner     agent.EventHook // agent's own hook, may be nil
}

// OnToolCallStart is called before a tool handler is invoked.
// It emits an EventAgentToolCallStart GraphEvent and delegates to the inner hook if present.
func (b *bridgeEventHook) OnToolCallStart(c *agent.Context, toolName string, input json.RawMessage) {
	if b.inner != nil {
		b.inner.OnToolCallStart(c, toolName, input)
	}
	b.graphHook.OnEvent(GraphEvent{
		Type:      EventAgentToolCallStart,
		Timestamp: time.Now(),
		NodeName:  b.nodeName,
		ToolName:  toolName,
		ToolInput: input,
	})
}

// OnToolCallEnd is called after a tool handler completes.
// It emits an EventAgentToolCallEnd GraphEvent and delegates to the inner hook if present.
func (b *bridgeEventHook) OnToolCallEnd(c *agent.Context, toolName string, output string, err error, duration time.Duration) {
	if b.inner != nil {
		b.inner.OnToolCallEnd(c, toolName, output, err, duration)
	}
	b.graphHook.OnEvent(GraphEvent{
		Type:         EventAgentToolCallEnd,
		Timestamp:    time.Now(),
		NodeName:     b.nodeName,
		ToolName:     toolName,
		ToolOutput:   output,
		ToolDuration: duration,
		Error:        err,
	})
}

// OnModelStart is called before the Provider.ConverseStream call.
// It emits an EventAgentModelStart GraphEvent and delegates to the inner hook if present.
func (b *bridgeEventHook) OnModelStart(c *agent.Context) {
	if b.inner != nil {
		b.inner.OnModelStart(c)
	}
	b.graphHook.OnEvent(GraphEvent{
		Type:      EventAgentModelStart,
		Timestamp: time.Now(),
		NodeName:  b.nodeName,
	})
}

// OnModelEnd is called after the Provider call completes.
// It emits an EventAgentModelEnd GraphEvent and delegates to the inner hook if present.
func (b *bridgeEventHook) OnModelEnd(c *agent.Context, stopReason string) {
	if b.inner != nil {
		b.inner.OnModelEnd(c, stopReason)
	}
	b.graphHook.OnEvent(GraphEvent{
		Type:       EventAgentModelEnd,
		Timestamp:  time.Now(),
		NodeName:   b.nodeName,
		StopReason: stopReason,
	})
}

// OnThinking is called for each thinking/reasoning chunk emitted by the model.
// It emits an EventAgentThinking GraphEvent and delegates to the inner hook if present.
func (b *bridgeEventHook) OnThinking(c *agent.Context, chunk string) {
	if b.inner != nil {
		b.inner.OnThinking(c, chunk)
	}
	b.graphHook.OnEvent(GraphEvent{
		Type:      EventAgentThinking,
		Timestamp: time.Now(),
		NodeName:  b.nodeName,
		Chunk:     chunk,
	})
}

// newBridgeEventHook creates a bridgeEventHook that forwards agent events to the graph's
// event hook. Returns nil if graphHook is nil (zero overhead when no graph event hook is configured).
// The inner parameter is the agent's own EventHook (may be nil).
func newBridgeEventHook(graphHook GraphEventHook, nodeName string, inner agent.EventHook) *bridgeEventHook {
	if graphHook == nil {
		return nil
	}
	return &bridgeEventHook{
		graphHook: graphHook,
		nodeName:  nodeName,
		inner:     inner,
	}
}

// NewBridgeEventHook creates a bridge event hook that forwards agent events to the
// graph's event hook. Use this when building custom node functions that need tool call
// and model event visibility in devtools. Returns nil if graphHook is nil.
func NewBridgeEventHook(graphHook GraphEventHook, nodeName string, inner agent.EventHook) agent.EventHook {
	b := newBridgeEventHook(graphHook, nodeName, inner)
	if b == nil {
		return nil
	}
	return b
}
