package graph

import "fmt"

// Node is a typed handle to a registered graph node. It is returned by
// Graph.Add() and Graph.AddAgent() and exposes methods for wiring, interrupt
// configuration, and metadata access — providing compile-time safety over
// raw string-based APIs.
type Node[S any] struct {
	name  string
	graph *Graph[S]
}

// Name returns the node's registration name.
func (n *Node[S]) Name() string { return n.name }

// String implements fmt.Stringer.
func (n *Node[S]) String() string { return n.name }

// InputKeys returns a copy of the node's declared input keys, including any
// synthetic keys added by Then() / Link().
func (n *Node[S]) InputKeys() []string {
	meta := n.graph.dataflow[n.name]
	out := make([]string, len(meta.InputKeys))
	copy(out, meta.InputKeys)
	return out
}

// OutputKeys returns a copy of the node's declared output keys.
func (n *Node[S]) OutputKeys() []string {
	meta := n.graph.dataflow[n.name]
	out := make([]string, len(meta.OutputKeys))
	copy(out, meta.OutputKeys)
	return out
}

// Then declares that this node must complete before the given node(s) begin.
// When multiple targets are provided, they will all run after this node completes
// (fork pattern). Equivalent to calling g.Connect(n.Name(), to.Name()) for each target.
//
//	fetch.Then(summarise, sentiment)  // fork: both run after fetch
//	summarise.Then(report)            // join: report waits for both
//	sentiment.Then(report)
func (n *Node[S]) Then(targets ...*Node[S]) error {
	for _, next := range targets {
		if next == nil {
			return &GraphValidationError{Message: "Then: target node must not be nil"}
		}
		if err := n.graph.Connect(n.name, next.name); err != nil {
			return err
		}
	}
	return nil
}

// After declares that this node must wait for all the given node(s) to complete
// before it begins (join pattern). Equivalent to calling source.Then(n) for each source.
//
//	report.After(summarise, sentiment)  // report waits for both
func (n *Node[S]) After(sources ...*Node[S]) error {
	for _, src := range sources {
		if src == nil {
			return &GraphValidationError{Message: "After: source node must not be nil"}
		}
		if err := src.graph.Connect(src.name, n.name); err != nil {
			return err
		}
	}
	return nil
}

// InterruptBefore marks this node to pause execution before it runs.
// Equivalent to g.InterruptBefore(n.Name()).
func (n *Node[S]) InterruptBefore() error {
	return n.graph.InterruptBefore(n.name)
}

// InterruptAfter marks this node to pause execution after it completes.
// Equivalent to g.InterruptAfter(n.Name()).
func (n *Node[S]) InterruptAfter() error {
	return n.graph.InterruptAfter(n.name)
}

// SetMeta attaches display metadata to this node.
// Equivalent to g.SetNodeMeta(n.Name(), meta).
func (n *Node[S]) SetMeta(meta NodeMeta) {
	n.graph.SetNodeMeta(n.name, meta)
}

// NodeInput declares the state keys a node reads. Use In() to construct.
// Future fields (validators, defaults, optional keys) can be added without
// breaking the API.
type NodeInput struct {
	Keys []string
}

// NodeOutput declares the state keys a node writes. Use Out() to construct.
// Future fields (schema, required keys) can be added without breaking the API.
type NodeOutput struct {
	Keys []string
}

// In declares the input keys (state keys a node reads).
// A node runs when all its input keys are present in the readiness set.
// Pass no arguments for entry nodes that have no dependencies.
func In(keys ...string) NodeInput { return NodeInput{Keys: keys} }

// Out declares the output keys (state keys a node writes).
// After a node completes, its output keys are added to the readiness set.
func Out(keys ...string) NodeOutput { return NodeOutput{Keys: keys} }

// DataFlowMeta declares the input and output keys for a node.
// This is the sole mechanism for expressing data dependencies.
type DataFlowMeta struct {
	InputKeys  []string
	OutputKeys []string
}

// DataFlowEdge represents a directed data dependency in the Structure API.
type DataFlowEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Key  string `json:"key"`
}

// Add registers a named node and returns a *Node[S] handle.
// Deprecated: Use Node() directly — it now returns (*Node[S], error).
func (g *Graph[S]) Add(name string, fn NodeFunc[S], opts ...NodeOpt) (*Node[S], error) {
	return g.Node(name, fn, opts...)
}

// Connect declares that node "from" must complete before node "to" begins,
// without requiring either node to read or write a meaningful state key.
// It generates a synthetic scheduling key (__connect:<from>→<to>) and appends
// it to "from"'s output keys and "to"'s input keys.
//
// Use Connect for pure sequencing — when the dependency is about ordering,
// not about data flowing between nodes. For data dependencies, prefer
// declaring real keys via In()/Out().
//
// Both nodes must already be registered. Returns an error if either node
// is not found.
func (g *Graph[S]) Connect(from, to string) error {
	fromMeta, fromOK := g.dataflow[from]
	if !fromOK {
		return &GraphValidationError{Message: fmt.Sprintf("Connect: source node %q is not registered", from)}
	}
	toMeta, toOK := g.dataflow[to]
	if !toOK {
		return &GraphValidationError{Message: fmt.Sprintf("Connect: target node %q is not registered", to)}
	}

	key := "__connect:" + from + "→" + to

	fromMeta.OutputKeys = append(fromMeta.OutputKeys, key)
	g.dataflow[from] = fromMeta

	toMeta.InputKeys = append(toMeta.InputKeys, key)
	g.dataflow[to] = toMeta

	return nil
}
