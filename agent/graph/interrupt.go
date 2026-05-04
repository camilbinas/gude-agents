package graph

import (
	"fmt"
)

// InterruptType indicates whether the interrupt fired before or after the node.
type InterruptType string

const (
	InterruptTypeBefore InterruptType = "before"
	InterruptTypeAfter  InterruptType = "after"
)

// InterruptResult is returned when execution pauses at an interrupt point.
type InterruptResult struct {
	NodeName   string        `json:"node_name"`
	Type       InterruptType `json:"type"`
	Checkpoint Checkpoint    `json:"checkpoint"`
}

// GraphInterruptError wraps an InterruptResult as an error so it can be
// returned from Run/Resume while preserving the error return signature.
type GraphInterruptError struct {
	Result InterruptResult
}

func (e *GraphInterruptError) Error() string {
	return fmt.Sprintf("graph: interrupted %s node %q", e.Result.Type, e.Result.NodeName)
}
