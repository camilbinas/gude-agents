package graph

import (
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
