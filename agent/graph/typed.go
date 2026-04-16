package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

// GraphState is an optional base struct that typed graph states can embed to get
// automatic token usage accumulation. When embedded, nodes can call AddUsage to
// accumulate token counts, and the graph will automatically propagate them to the
// underlying usage tracker via the __usage__ mechanism.
//
// Usage:
//
//	type MyState struct {
//	    graph.GraphState          // embed for automatic token tracking
//	    Topic   string `json:"topic"`
//	    Summary string `json:"summary"`
//	}
//
//	// In a node:
//	result, usage, err := myAgent.Invoke(ctx, s.Topic)
//	s.AddUsage(usage)  // accumulates into the graph's token counter
type GraphState struct {
	pendingUsage agent.TokenUsage `json:"-"`
}

// AddUsage accumulates token usage from an agent call into the graph's counter.
func (g *GraphState) AddUsage(u agent.TokenUsage) {
	g.pendingUsage.InputTokens += u.InputTokens
	g.pendingUsage.OutputTokens += u.OutputTokens
}

// usageCarrier is the interface used internally to extract pending usage from a state.
type usageCarrier interface {
	getPendingUsage() agent.TokenUsage
	clearPendingUsage()
}

func (g *GraphState) getPendingUsage() agent.TokenUsage { return g.pendingUsage }
func (g *GraphState) clearPendingUsage()                { g.pendingUsage = agent.TokenUsage{} }

// TypedNodeFunc is the typed equivalent of NodeFunc.
// It receives and returns a concrete state struct S instead of map[string]any.
type TypedNodeFunc[S any] func(ctx context.Context, state S) (S, error)

// TypedRouterFunc is the typed equivalent of RouterFunc.
type TypedRouterFunc[S any] func(ctx context.Context, state S) (string, error)

// TypedGraphResult is returned by TypedGraph.Run on success.
type TypedGraphResult[S any] struct {
	State S
	Usage agent.TokenUsage
}

// TypedGraph is a generic wrapper around Graph that lets nodes work with a
// concrete state struct S instead of map[string]any.
//
// S must be a struct whose fields are JSON-serialisable. The bridge between S
// and the underlying State map is a JSON round-trip: S → JSON → State on the
// way in, State → JSON → S on the way out. This keeps the execution engine
// unchanged and adds no reflection beyond what encoding/json already does.
//
// Embed graph.GraphState in S to get automatic token usage accumulation:
//
//	type MyState struct {
//	    graph.GraphState
//	    Topic   string `json:"topic"`
//	    Summary string `json:"summary"`
//	}
type TypedGraph[S any] struct {
	inner *Graph
}

// NewTypedGraph creates a TypedGraph with the given options.
// Returns an error if any option is invalid (same rules as NewGraph).
func NewTypedGraph[S any](opts ...GraphOption) (*TypedGraph[S], error) {
	inner, err := NewGraph(opts...)
	if err != nil {
		return nil, err
	}
	return &TypedGraph[S]{inner: inner}, nil
}

// AddNode registers a typed node. Returns an error on empty name, nil fn, or duplicate name.
func (g *TypedGraph[S]) AddNode(name string, fn TypedNodeFunc[S]) error {
	if fn == nil {
		return &GraphValidationError{Message: fmt.Sprintf("node %q: fn must not be nil", name)}
	}
	return g.inner.AddNode(name, func(ctx context.Context, raw State) (State, error) {
		s, err := stateToTyped[S](raw)
		if err != nil {
			return nil, fmt.Errorf("node %q: decode state: %w", name, err)
		}
		out, err := fn(ctx, s)
		if err != nil {
			return nil, err
		}
		result, err := typedToState(out)
		if err != nil {
			return nil, err
		}
		// If the state embeds GraphState, extract pending usage and write it to
		// __usage__ so the graph engine can accumulate it automatically.
		if carrier, ok := any(&out).(usageCarrier); ok {
			if u := carrier.getPendingUsage(); u.Total() > 0 {
				result["__usage__"] = u
				carrier.clearPendingUsage()
			}
		}
		return result, nil
	})
}

// SetEntry designates the entry node. Validated at Run time.
func (g *TypedGraph[S]) SetEntry(name string) {
	g.inner.SetEntry(name)
}

// AddEdge registers a static edge from → to.
func (g *TypedGraph[S]) AddEdge(from, to string) error {
	return g.inner.AddEdge(from, to)
}

// AddConditionalEdge registers a conditional edge driven by a typed router.
func (g *TypedGraph[S]) AddConditionalEdge(from string, router TypedRouterFunc[S]) error {
	if router == nil {
		return &GraphValidationError{Message: fmt.Sprintf("AddConditionalEdge: router for %q must not be nil", from)}
	}
	return g.inner.AddConditionalEdge(from, func(ctx context.Context, raw State) (string, error) {
		s, err := stateToTyped[S](raw)
		if err != nil {
			return "", fmt.Errorf("conditional edge %q: decode state: %w", from, err)
		}
		return router(ctx, s)
	})
}

// AddFork registers a parallel fork from one node to multiple targets.
func (g *TypedGraph[S]) AddFork(from string, targets []string) error {
	return g.inner.AddFork(from, targets)
}

// AddJoin registers a join barrier: node waits for all predecessors.
func (g *TypedGraph[S]) AddJoin(node string, predecessors []string) error {
	return g.inner.AddJoin(node, predecessors)
}

// Run validates the graph and executes it from the entry node.
func (g *TypedGraph[S]) Run(ctx context.Context, initial S) (TypedGraphResult[S], error) {
	raw, err := typedToState(initial)
	if err != nil {
		return TypedGraphResult[S]{}, fmt.Errorf("Run: encode initial state: %w", err)
	}

	result, err := g.inner.Run(ctx, raw)
	if err != nil {
		return TypedGraphResult[S]{}, err
	}

	final, err := stateToTyped[S](result.State)
	if err != nil {
		return TypedGraphResult[S]{}, fmt.Errorf("Run: decode final state: %w", err)
	}

	return TypedGraphResult[S]{State: final, Usage: result.Usage}, nil
}

// --- bridge helpers ---

// typedToState converts a struct to a State map via JSON.
func typedToState[S any](s S) (State, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return State(m), nil
}

// stateToTyped converts a State map back to a struct via JSON.
func stateToTyped[S any](raw State) (S, error) {
	var zero S
	b, err := json.Marshal(map[string]any(raw))
	if err != nil {
		return zero, err
	}
	var s S
	if err := json.Unmarshal(b, &s); err != nil {
		return zero, err
	}
	return s, nil
}
