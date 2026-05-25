package graph

import (
	"encoding/json"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// EventType identifies the kind of lifecycle event emitted during graph execution.
type EventType string

const (
	EventNodeStarted     EventType = "NodeStarted"
	EventNodeCompleted   EventType = "NodeCompleted"
	EventCheckpointSaved EventType = "CheckpointSaved"
	EventInterruptFired  EventType = "InterruptFired"
	EventResumed         EventType = "Resumed"
	EventRewindCompleted EventType = "RewindCompleted"
	EventGraphStarted    EventType = "GraphStarted"
	EventGraphCompleted  EventType = "GraphCompleted"

	// Agent-level event types for tool calls, model interactions, and streaming.
	EventAgentToolCallStart        EventType = "AgentToolCallStart"
	EventAgentToolCallEnd          EventType = "AgentToolCallEnd"
	EventAgentModelStart           EventType = "AgentModelStart"
	EventAgentModelEnd             EventType = "AgentModelEnd"
	EventAgentThinking             EventType = "AgentThinking"
	EventAgentStreaming            EventType = "AgentStreaming"
	EventAgentIterationStart       EventType = "AgentIterationStart"
	EventAgentIterationEnd         EventType = "AgentIterationEnd"
	EventAgentMaxIterationsReached EventType = "AgentMaxIterationsReached"

	// EventCustom carries a user-defined event emitted from inside a graph
	// node function (or, by way of the agent->graph bridge, from inside an
	// Agent node's tool handler) via graph.EmitEvent. The payload is opaque
	// JSON; consumers discriminate on CustomName. The runtime never emits
	// this variant itself.
	EventCustom EventType = "Custom"
)

// GraphEvent is a structured event emitted by the Graph during execution.
// It carries an event type, timestamp, and contextual data relevant to the event.
// Consumers should switch on the Type field to determine which fields are populated.
type GraphEvent struct {
	Type          EventType        `json:"type,omitempty"`
	Timestamp     time.Time        `json:"timestamp,omitempty"`
	NodeName      string           `json:"node_name,omitempty"`
	Version       int              `json:"version,omitempty"`
	StateSnapshot State            `json:"state_snapshot,omitempty"`
	Usage         agent.TokenUsage `json:"usage,omitempty"`
	InterruptType InterruptType    `json:"interrupt_type,omitempty"`
	Error         error            `json:"error,omitempty"`
	ThreadID      string           `json:"thread_id,omitempty"`

	// Agent-level event fields for tool calls, model interactions, and streaming.
	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput   string          `json:"tool_output,omitempty"`
	ToolDuration time.Duration   `json:"tool_duration,omitempty"`
	StopReason   string          `json:"stop_reason,omitempty"`
	Chunk        string          `json:"chunk,omitempty"`

	// Agent iteration-loop fields.
	Iteration         int           `json:"iteration,omitempty"`
	ToolCount         int           `json:"tool_count,omitempty"`
	IsFinal           bool          `json:"is_final,omitempty"`
	IterationDuration time.Duration `json:"iteration_duration,omitempty"`
	IterationLimit    int           `json:"iteration_limit,omitempty"`

	// Custom-event payload (EventCustom). CustomName is a free-form,
	// dot-namespaced tag chosen by the emitter (e.g. "rag.retrieved").
	// CustomPayload is the JSON-encoded user payload. NodeName is set to
	// the graph node that emitted the event, including events bridged up
	// from an Agent node's inner tool handlers.
	CustomName    string          `json:"custom_name,omitempty"`
	CustomPayload json.RawMessage `json:"custom_payload,omitempty"`
}

// GraphEventHook is an optional interface for receiving structured events at all
// graph execution lifecycle points. This enables real-time frontend visualization,
// logging, metrics collection, and debugging of graph execution.
//
// OnEvent is called synchronously by the Graph at each lifecycle point.
// Implementations are responsible for ensuring non-blocking behavior (e.g.,
// buffered channel sends, async dispatch) to avoid slowing down graph execution.
// Long-running operations inside OnEvent will block the graph's progress.
type GraphEventHook interface {
	// OnEvent receives a structured event at each graph execution lifecycle point.
	// It is called synchronously; the hook must not block.
	OnEvent(event GraphEvent)
}
