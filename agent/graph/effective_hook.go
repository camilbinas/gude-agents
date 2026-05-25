package graph

import "context"

// effectiveEventHook is the GraphEventHook that should receive every event
// emitted during a particular Run / Resume call. It composes the graph-level
// hook (set via WithEventHook / SetEventHook) with any per-call hook injected
// via runConfig.extraEventHook (e.g. by RunEventStream / ResumeEventStream).
//
// Either field may be nil; the helper methods on this type elide nil dispatches
// so call sites don't have to.
type effectiveEventHook struct {
	graphHook GraphEventHook
	extraHook GraphEventHook
}

// emit dispatches an event to whichever hooks are non-nil.
func (h effectiveEventHook) emit(event GraphEvent) {
	if h.graphHook != nil {
		h.graphHook.OnEvent(event)
	}
	if h.extraHook != nil {
		h.extraHook.OnEvent(event)
	}
}

// isZero reports whether neither hook is set, so callers can short-circuit
// snapshot building when there's no observer.
func (h effectiveEventHook) isZero() bool {
	return h.graphHook == nil && h.extraHook == nil
}

// asGraphEventHook returns a single GraphEventHook that fans events into
// both underlying hooks. Returns nil if neither hook is configured. This is
// used to bridge the effective hook into per-node code that expects a single
// GraphEventHook (e.g. the agent-node bridge).
func (h effectiveEventHook) asGraphEventHook() GraphEventHook {
	if h.isZero() {
		return nil
	}
	return effectiveHookAdapter{h: h}
}

// effectiveHookAdapter implements GraphEventHook by delegating to an
// effectiveEventHook value. Used so we can hand a single hook instance to
// agent-node bridge code while still fanning out to graph + per-call hooks.
type effectiveHookAdapter struct {
	h effectiveEventHook
}

func (a effectiveHookAdapter) OnEvent(e GraphEvent) { a.h.emit(e) }

// effectiveHookKey is the context.Context key under which the effective hook
// is stashed for the duration of a Run / Resume call. Agent nodes read from
// this key so they pick up per-call hooks (e.g. RunEventStream's channel)
// instead of just g.eventHook.
type effectiveHookKey struct{}

// withEffectiveHook returns ctx augmented with the given effective hook.
// Callers that want to read it back use lookupEffectiveHook.
func withEffectiveHook(ctx context.Context, h effectiveEventHook) context.Context {
	if h.isZero() {
		return ctx
	}
	return context.WithValue(ctx, effectiveHookKey{}, h)
}

// lookupEffectiveHook returns the effective hook stored on ctx by
// withEffectiveHook. The graph-level fallback is used when no run-scoped
// hook is present (i.e. when the graph was invoked via plain Run/Resume on
// older code paths that didn't set the context value).
func lookupEffectiveHook(ctx context.Context, fallbackGraphHook GraphEventHook) effectiveEventHook {
	if v, ok := ctx.Value(effectiveHookKey{}).(effectiveEventHook); ok {
		return v
	}
	return effectiveEventHook{graphHook: fallbackGraphHook}
}
