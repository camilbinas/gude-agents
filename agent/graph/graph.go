package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// State is the shared data container passed between nodes.
// It is the default type parameter for untyped graphs: Graph[State].
type State = map[string]any

// route[S] is a sealed union: exactly one field is set.
type route[S any] struct {
	static      string        // static edge target
	conditional RouterFunc[S] // conditional edge router
	fork        []string      // fork targets
}

// GraphConfigurator is implemented by Graph[S] for any S.
// GraphOption functions call methods on this interface, allowing options
// to remain non-generic while working with Graph[S] for any S.
type GraphConfigurator interface {
	setMaxIter(n int)
	setCheckpointer(cp GraphCheckpointer)
	setCheckpointOnInterruptOnly()
	setEventHook(hook GraphEventHook)
	SetGraphTracingHook(h GraphTracingHook)
	SetGraphMetricsHook(h GraphMetricsHook)
	SetGraphLoggingHook(h GraphLoggingHook)
}

// Graph[S] is a directed graph of named nodes connected by routing rules.
// S is the state type passed between nodes.
// When S is State (map[string]any), operations are zero-cost direct map manipulations.
// When S is a custom struct, JSON serialization occurs only at checkpoint/event boundaries.
// Fields are written only during construction; after that they are read-only,
// making concurrent Run calls safe.
type Graph[S any] struct {
	nodes       map[string]NodeFunc[S]
	entry       string
	routes      map[string]route[S] // one route per source node
	joins       map[string][]string // node → required predecessors
	maxIter     int
	ops         stateOps[S]
	nodeMeta    map[string]NodeMeta // optional metadata per node
	tracingHook GraphTracingHook    // nil = no tracing
	metricsHook GraphMetricsHook    // nil = no metrics
	loggingHook GraphLoggingHook    // nil = no structured logging

	checkpointer              GraphCheckpointer // nil = no checkpointing
	checkpointOnInterruptOnly bool
	interruptBefore           map[string]bool
	interruptAfter            map[string]bool
	eventHook                 GraphEventHook // nil = no event emission
}

// NodeMeta holds optional metadata for a node.
type NodeMeta struct {
	Label    string `json:"label,omitempty"`    // human-readable name
	Agent    string `json:"agent,omitempty"`    // agent name
	Provider string `json:"provider,omitempty"` // provider name
	Model    string `json:"model,omitempty"`    // model ID
}

// --- GraphConfigurator implementation for Graph[S] ---

func (g *Graph[S]) setMaxIter(n int)                     { g.maxIter = n }
func (g *Graph[S]) setCheckpointer(cp GraphCheckpointer) { g.checkpointer = cp }
func (g *Graph[S]) setCheckpointOnInterruptOnly()        { g.checkpointOnInterruptOnly = true }
func (g *Graph[S]) setEventHook(hook GraphEventHook)     { g.eventHook = hook }

// GraphOption configures a Graph[S] for any S.
// It operates on the GraphConfigurator interface so that options remain non-generic.
type GraphOption func(GraphConfigurator) error

// WithMaxIterations sets the maximum number of node executions per Run.
// Returns an error if n < 1.
func WithMaxIterations(n int) GraphOption {
	return func(g GraphConfigurator) error {
		if n < 1 {
			return &GraphValidationError{Message: "MaxIterations must be >= 1"}
		}
		g.setMaxIter(n)
		return nil
	}
}

// WithCheckpointer sets the GraphCheckpointer for persistent execution.
func WithCheckpointer(cp GraphCheckpointer) GraphOption {
	return func(g GraphConfigurator) error {
		if cp == nil {
			return &GraphValidationError{Message: "WithCheckpointer: checkpointer must not be nil"}
		}
		g.setCheckpointer(cp)
		return nil
	}
}

// WithCheckpointOnInterruptOnly limits checkpointing to interrupt points only.
// Requires WithCheckpointer to also be set.
func WithCheckpointOnInterruptOnly() GraphOption {
	return func(g GraphConfigurator) error {
		g.setCheckpointOnInterruptOnly()
		return nil
	}
}

// WithEventHook sets the GraphEventHook for receiving structured execution events.
// When set, the graph emits GraphEvent values at each lifecycle point (graph start/end,
// node start/end, checkpoint saves, interrupts, resume, rewind).
// When nil or not set, no events are emitted and there is zero overhead.
func WithEventHook(hook GraphEventHook) GraphOption {
	return func(g GraphConfigurator) error {
		if hook == nil {
			return &GraphValidationError{Message: "WithEventHook: hook must not be nil"}
		}
		g.setEventHook(hook)
		return nil
	}
}

// New creates a configured Graph[S]. The strategy for state operations is
// selected at construction time based on S:
// - When S is State (map[string]any): uses mapStateOps (zero-cost)
// - When S is any other type: uses jsonStateOps[S] (JSON at boundaries only)
func New[S any](opts ...GraphOption) (*Graph[S], error) {
	g := &Graph[S]{
		nodes:           make(map[string]NodeFunc[S]),
		routes:          make(map[string]route[S]),
		joins:           make(map[string][]string),
		maxIter:         100,
		nodeMeta:        make(map[string]NodeMeta),
		interruptBefore: make(map[string]bool),
		interruptAfter:  make(map[string]bool),
	}

	// Strategy selection: detect if S is map[string]any at construction time.
	if isMapStringAny[S]() {
		g.ops = any(mapStateOps{}).(stateOps[S])
	} else {
		g.ops = jsonStateOps[S]{}
	}

	for _, opt := range opts {
		if err := opt(g); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// AddNode registers a named node. Returns an error on empty name, nil fn, or duplicate name.
func (g *Graph[S]) AddNode(name string, fn NodeFunc[S]) error {
	if name == "" {
		return &GraphValidationError{Message: "node name must not be empty"}
	}
	if fn == nil {
		return &GraphValidationError{Message: fmt.Sprintf("node %q: fn must not be nil", name)}
	}
	if _, exists := g.nodes[name]; exists {
		return &GraphValidationError{Message: fmt.Sprintf("node %q already registered", name)}
	}
	g.nodes[name] = fn
	return nil
}

// SetEntry designates the entry node. Validated at Run time.
func (g *Graph[S]) SetEntry(name string) {
	g.entry = name
}

// AddEdge registers a static edge from → to. Returns an error on empty from or to.
func (g *Graph[S]) AddEdge(from, to string) error {
	if from == "" {
		return &GraphValidationError{Message: "AddEdge: from must not be empty"}
	}
	if to == "" {
		return &GraphValidationError{Message: "AddEdge: to must not be empty"}
	}
	g.routes[from] = route[S]{static: to}
	return nil
}

// AddConditionalEdge registers a conditional edge from the given node.
func (g *Graph[S]) AddConditionalEdge(from string, router RouterFunc[S]) error {
	if from == "" {
		return &GraphValidationError{Message: "AddConditionalEdge: from must not be empty"}
	}
	if router == nil {
		return &GraphValidationError{Message: fmt.Sprintf("AddConditionalEdge: router for %q must not be nil", from)}
	}
	g.routes[from] = route[S]{conditional: router}
	return nil
}

// AddFork registers a parallel fork from one node to multiple targets.
// Returns an error if fewer than 2 targets are provided.
func (g *Graph[S]) AddFork(from string, targets []string) error {
	if from == "" {
		return &GraphValidationError{Message: "AddFork: from must not be empty"}
	}
	if len(targets) < 2 {
		return &GraphValidationError{Message: fmt.Sprintf("AddFork: node %q requires at least 2 targets", from)}
	}
	g.routes[from] = route[S]{fork: targets}
	return nil
}

// AddJoin registers a join barrier: node waits for all predecessors.
// Returns an error if fewer than 2 predecessors are provided.
func (g *Graph[S]) AddJoin(node string, predecessors []string) error {
	if node == "" {
		return &GraphValidationError{Message: "AddJoin: node must not be empty"}
	}
	if len(predecessors) < 2 {
		return &GraphValidationError{Message: fmt.Sprintf("AddJoin: node %q requires at least 2 predecessors", node)}
	}
	g.joins[node] = predecessors
	return nil
}

// GraphValidationError is returned when graph structure is invalid.
type GraphValidationError struct {
	Message string
}

func (e *GraphValidationError) Error() string { return "graph validation: " + e.Message }

// GraphIterationError is returned when MaxIterations is exceeded.
type GraphIterationError struct {
	Limit int
}

func (e *GraphIterationError) Error() string {
	return fmt.Sprintf("graph: max iterations (%d) exceeded", e.Limit)
}

// validate checks the graph structure before execution.
// It is called at the start of every Run.
func (g *Graph[S]) validate() error {
	// 1. Entry node must be registered.
	if _, ok := g.nodes[g.entry]; !ok {
		return &GraphValidationError{Message: fmt.Sprintf("entry node %q is not registered", g.entry)}
	}

	// 2–5. Check all routes.
	for node, r := range g.routes {
		// Source node must be registered.
		if _, ok := g.nodes[node]; !ok {
			return &GraphValidationError{Message: fmt.Sprintf("route source node %q is not registered", node)}
		}

		// Conflict check: at most one field of the route union may be set.
		if r.static != "" && r.conditional != nil {
			return &GraphValidationError{Message: fmt.Sprintf("node %q has conflicting routing rules (static and conditional)", node)}
		}
		if r.static != "" && len(r.fork) > 0 {
			return &GraphValidationError{Message: fmt.Sprintf("node %q has conflicting routing rules (static and fork)", node)}
		}
		if r.conditional != nil && len(r.fork) > 0 {
			return &GraphValidationError{Message: fmt.Sprintf("node %q has conflicting routing rules (conditional and fork)", node)}
		}

		// 2. Static edge target must be registered.
		if r.static != "" {
			if _, ok := g.nodes[r.static]; !ok {
				return &GraphValidationError{Message: fmt.Sprintf("node %q static edge target %q is not registered", node, r.static)}
			}
		}

		// 3. Fork targets must be registered.
		for _, target := range r.fork {
			if _, ok := g.nodes[target]; !ok {
				return &GraphValidationError{Message: fmt.Sprintf("node %q fork target %q is not registered", node, target)}
			}
		}
	}

	// 4. Join predecessors must be registered.
	for node, preds := range g.joins {
		for _, pred := range preds {
			if _, ok := g.nodes[pred]; !ok {
				return &GraphValidationError{Message: fmt.Sprintf("join node %q predecessor %q is not registered", node, pred)}
			}
		}
	}

	// 6. MaxIterations must be >= 1.
	if g.maxIter < 1 {
		return &GraphValidationError{Message: "MaxIterations must be >= 1"}
	}

	// 7. checkpointOnInterruptOnly requires a checkpointer.
	if g.checkpointOnInterruptOnly && g.checkpointer == nil {
		return &GraphValidationError{Message: "WithCheckpointOnInterruptOnly requires a checkpointer to be configured"}
	}

	return nil
}

// Run validates the graph and executes it from the entry node.
// It accepts optional RunOption values to configure the execution (e.g., WithThreadID).
func (g *Graph[S]) Run(ctx context.Context, initial S, opts ...RunOption) (Result[S], error) {
	if err := g.validate(); err != nil {
		return Result[S]{}, err
	}

	// Parse run options.
	var cfg runConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// When a checkpointer is configured, a thread ID is required.
	if g.checkpointer != nil && cfg.threadID == "" {
		return Result[S]{}, ErrThreadIDRequired
	}

	// Start graph tracing span if hook is set.
	var finishTrace func(err error, iterations int)
	if g.tracingHook != nil {
		ctx, finishTrace = g.tracingHook.OnGraphRunStart(ctx)
	}

	// Start graph metrics tracking if metrics hook is set.
	var finishMetrics func(err error, iterations int)
	if g.metricsHook != nil {
		finishMetrics = g.metricsHook.OnGraphRunStart()
	}

	// Start graph logging if logging hook is set.
	var graphRunStart time.Time
	if g.loggingHook != nil {
		g.loggingHook.OnGraphRunStart()
		graphRunStart = time.Now()
	}

	exec := &runExec[S]{
		graph:     g,
		state:     g.ops.copy(initial),
		ops:       g.ops,
		nodes:     g.nodes,
		routes:    g.routes,
		completed: make(map[string]bool),
		workQueue: []string{g.entry},
		threadID:  cfg.threadID,
	}

	err := exec.execute(ctx)

	// Emit GraphCompleted event with final state, usage, and error (if any).
	if g.eventHook != nil {
		snapshot, _ := g.ops.toMap(exec.state)
		g.eventHook.OnEvent(GraphEvent{
			Type:          EventGraphCompleted,
			Timestamp:     time.Now(),
			StateSnapshot: snapshot,
			Usage:         exec.usage,
			ThreadID:      cfg.threadID,
			Error:         err,
		})
	}

	if finishTrace != nil {
		finishTrace(err, exec.iterations)
	}
	if finishMetrics != nil {
		finishMetrics(err, exec.iterations)
	}
	if g.loggingHook != nil {
		g.loggingHook.OnGraphRunEnd(err, exec.iterations, exec.usage, time.Since(graphRunStart))
	}

	if err != nil {
		return Result[S]{}, err
	}

	return Result[S]{
		State: exec.state,
		Usage: exec.usage,
	}, nil
}

// SetGraphTracingHook sets the tracing hook on the graph.
// Called by the tracing submodule's GraphOption.
func (g *Graph[S]) SetGraphTracingHook(h GraphTracingHook) {
	g.tracingHook = h
}

// SetGraphMetricsHook sets the metrics hook on the graph.
// Called by the metrics submodule's GraphOption.
func (g *Graph[S]) SetGraphMetricsHook(h GraphMetricsHook) {
	g.metricsHook = h
}

// GetGraphMetricsHook returns the graph's metrics hook, or nil if none is set.
func (g *Graph[S]) GetGraphMetricsHook() GraphMetricsHook {
	return g.metricsHook
}

// SetGraphLoggingHook sets the logging hook on the graph.
// Called by the logging submodule's GraphOption.
func (g *Graph[S]) SetGraphLoggingHook(h GraphLoggingHook) {
	g.loggingHook = h
}

// GetGraphLoggingHook returns the graph's logging hook, or nil if none is set.
func (g *Graph[S]) GetGraphLoggingHook() GraphLoggingHook {
	return g.loggingHook
}

// SetNodeMeta attaches display metadata to a registered node.
// This metadata is included in Structure() for visualization tools.
func (g *Graph[S]) SetNodeMeta(name string, meta NodeMeta) {
	g.nodeMeta[name] = meta
}

// InterruptBefore marks a node to pause execution before it runs.
// Returns an error if the node is not registered.
func (g *Graph[S]) InterruptBefore(name string) error {
	if _, ok := g.nodes[name]; !ok {
		return &GraphValidationError{
			Message: fmt.Sprintf("InterruptBefore: node %q is not registered", name),
		}
	}
	g.interruptBefore[name] = true
	return nil
}

// InterruptAfter marks a node to pause execution after it completes.
// Returns an error if the node is not registered.
func (g *Graph[S]) InterruptAfter(name string) error {
	if _, ok := g.nodes[name]; !ok {
		return &GraphValidationError{
			Message: fmt.Sprintf("InterruptAfter: node %q is not registered", name),
		}
	}
	g.interruptAfter[name] = true
	return nil
}

// CopyState returns a shallow copy of s.
func CopyState(s State) State {
	out := make(State, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// mergeState merges patch into base (mutates base).
func mergeState(base, patch State) {
	for k, v := range patch {
		base[k] = v
	}
}

// mergeDiff merges only the keys from branch that differ from snapshot into base.
// This prevents fork branches from overwriting each other's results with zero values.
func mergeDiff(base, snapshot, branch State) {
	for k, v := range branch {
		snapshotVal, existed := snapshot[k]
		if !existed {
			// New key added by branch.
			base[k] = v
		} else if !jsonEqual(snapshotVal, v) {
			// Key existed but value changed.
			base[k] = v
		}
	}
}

// jsonEqual compares two values by JSON representation.
// Used for fork/join diff detection.
func jsonEqual(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// copyCompleted returns a shallow copy of a completed-nodes map.
func copyCompleted(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
