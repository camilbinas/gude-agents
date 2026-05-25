package graph

import (
	"context"
	"encoding/json"
	"time"
)

// EmitEvent emits a user-defined event onto the active RunEventStream channel
// from inside a graph node function. When no event stream is active — i.e.
// the run was started with plain Run / Resume and no graph-level
// GraphEventHook is configured — the call is a no-op.
//
// The ctx passed in must be the context.Context received by the node
// function; the graph runtime stashes the effective hook on it. NodeName
// is not required: the runtime sets it on the GraphEvent based on the
// emitting node's identity when known. For events emitted outside a node
// the field will be empty.
//
// name should be a short, dot-namespaced tag chosen by the emitter
// (e.g. "rag.retrieved", "score.computed"). payload is JSON-marshalled;
// pass any value json.Marshal can handle. Marshal failures silently drop
// the event so EmitEvent never disturbs graph execution.
//
// Custom events are delivered on the same channel as the graph's built-in
// events with Type=EventCustom. They're also bridged from agent nodes:
// calls to *agent.Context.EmitEvent inside an agent's tool handler reach
// the graph's RunEventStream automatically with NodeName populated by the
// bridging node name.
//
// Safe for concurrent use.
func EmitEvent(ctx context.Context, name string, payload any) {
	hook := lookupEffectiveHook(ctx, nil).asGraphEventHook()
	if hook == nil {
		return
	}
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return
		}
		raw = b
	}
	hook.OnEvent(GraphEvent{
		Type:          EventCustom,
		Timestamp:     time.Now(),
		NodeName:      currentNodeFromContext(ctx),
		CustomName:    name,
		CustomPayload: raw,
	})
}

// currentNodeFromContext returns the node name attached to ctx by the engine
// when it invokes a node function. Returns the empty string when the caller
// is outside a graph node (e.g. before Run started or from a goroutine that
// dropped the context value).
func currentNodeFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(currentNodeKey{}).(string); ok {
		return v
	}
	return ""
}

// currentNodeKey is the context key the engine uses to advertise which node
// is currently executing. The engine sets it before dispatching a node
// function so EmitEvent can attribute custom events without requiring the
// caller to pass the node name explicitly.
type currentNodeKey struct{}

// withCurrentNode returns ctx augmented with the given node name. Used by
// the engine wrapper around node-function dispatch.
func withCurrentNode(ctx context.Context, nodeName string) context.Context {
	if nodeName == "" {
		return ctx
	}
	return context.WithValue(ctx, currentNodeKey{}, nodeName)
}
