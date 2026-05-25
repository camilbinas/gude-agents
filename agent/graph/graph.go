package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// State is the shared data container passed between nodes.
// It is the default type parameter for untyped graphs: Graph[State].
type State = map[string]any

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
	nodes    map[string]NodeFunc[S]
	entry    string
	dataflow map[string]DataFlowMeta // node name → I/O declarations
	maxIter  int
	ops      stateOps[S]
	nodeMeta map[string]NodeMeta // optional metadata per node

	tracingHook GraphTracingHook // nil = no tracing
	metricsHook GraphMetricsHook // nil = no metrics
	loggingHook GraphLoggingHook // nil = no structured logging

	checkpointer              GraphCheckpointer // nil = no checkpointing
	checkpointOnInterruptOnly bool
	interruptBefore           map[string]bool
	interruptAfter            map[string]bool
	eventHook                 GraphEventHook // nil = no event emission

	agentNodes map[string]*agent.Agent // node name → agent for dynamic metadata
}

// NodeMeta holds optional metadata for a node.
type NodeMeta struct {
	Label    string   `json:"label,omitempty"`    // human-readable name
	Agent    string   `json:"agent,omitempty"`    // agent name
	Provider string   `json:"provider,omitempty"` // provider name
	Model    string   `json:"model,omitempty"`    // model ID
	Tools    []string `json:"tools,omitempty"`    // tool names
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
		dataflow:        make(map[string]DataFlowMeta),
		maxIter:         100,
		nodeMeta:        make(map[string]NodeMeta),
		interruptBefore: make(map[string]bool),
		interruptAfter:  make(map[string]bool),
		agentNodes:      make(map[string]*agent.Agent),
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

// addNode registers a named node internally. Returns an error on empty name, nil fn, or duplicate name.
// This is used by Agent registration and by NodeWithKeys.
func (g *Graph[S]) addNode(name string, fn NodeFunc[S]) error {
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

// NodeOpt configures I/O declarations for a node. Use In() and Out() to create.
type NodeOpt interface {
	applyNode(in *[]string, out *[]string)
}

func (n NodeInput) applyNode(in *[]string, _ *[]string)   { *in = append(*in, n.Keys...) }
func (n NodeOutput) applyNode(_ *[]string, out *[]string) { *out = append(*out, n.Keys...) }

// RegisterNode registers a named node with optional I/O key declarations.
// This is the string-based registration API — use Node() for the handle-based API.
//
//	g.RegisterNode("fetch", fetchFn)                          // no keys, wire with Connect
//	g.RegisterNode("fetch", fetchFn, graph.In(), graph.Out("article"))  // explicit keys
func (g *Graph[S]) RegisterNode(name string, fn NodeFunc[S], opts ...NodeOpt) error {
	var inKeys, outKeys []string
	for _, opt := range opts {
		opt.applyNode(&inKeys, &outKeys)
	}

	// Validate keys are non-empty strings.
	for _, k := range inKeys {
		if k == "" {
			return &GraphValidationError{Message: fmt.Sprintf("node %q: keys must be non-empty strings", name)}
		}
	}
	for _, k := range outKeys {
		if k == "" {
			return &GraphValidationError{Message: fmt.Sprintf("node %q: keys must be non-empty strings", name)}
		}
	}

	if err := g.addNode(name, fn); err != nil {
		return err
	}

	g.dataflow[name] = DataFlowMeta{
		InputKeys:  inKeys,
		OutputKeys: outKeys,
	}
	return nil
}

// Node registers a named node and returns a *Node[S] handle for type-safe wiring,
// interrupt configuration, and metadata access. In/Out declarations are optional —
// omit them when using Then() for pure sequencing.
//
//	fetch, _ := g.Node("fetch", fetchFn)
//	process, _ := g.Node("process", processFn)
//	fetch.Then(process)
//
//	// With data-flow keys:
//	fetch, _ := g.Node("fetch", fetchFn, graph.Out("article"))
//	report, _ := g.Node("report", reportFn, graph.In("article"), graph.Out("output"))
func (g *Graph[S]) Node(name string, fn NodeFunc[S], opts ...NodeOpt) (*Node[S], error) {
	if err := g.RegisterNode(name, fn, opts...); err != nil {
		return nil, err
	}
	return &Node[S]{name: name, graph: g}, nil
}

// Start designates the entry node explicitly. Optional — if not called,
// the entry node is auto-detected as the sole node with empty input keys.
func (g *Graph[S]) Start(name string) {
	g.entry = name
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
	// 1. Determine entry node.
	if g.entry == "" {
		// Auto-detect: find nodes with empty input keys.
		var candidates []string
		for name, meta := range g.dataflow {
			if len(meta.InputKeys) == 0 {
				candidates = append(candidates, name)
			}
		}
		if len(candidates) == 0 {
			return &GraphValidationError{Message: "no entry node: either call Start() or register a node with empty input keys"}
		}
		if len(candidates) == 1 {
			g.entry = candidates[0]
		} else {
			// Multiple root nodes: pick the first alphabetically as the formal entry,
			// the rest will be scheduled immediately (they have empty input keys).
			sort.Strings(candidates)
			g.entry = candidates[0]
		}
	}

	if _, ok := g.nodes[g.entry]; !ok {
		return &GraphValidationError{Message: fmt.Sprintf("entry node %q is not registered", g.entry)}
	}

	// 2. MaxIterations must be >= 1.
	if g.maxIter < 1 {
		return &GraphValidationError{Message: "MaxIterations must be >= 1"}
	}

	// 3. checkpointOnInterruptOnly requires a checkpointer.
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

	// Extract initial state keys for data-flow validation and readiness.
	initialKeys := make(map[string]bool)      // for validation: all keys present in state
	initialReadyKeys := make(map[string]bool) // for runtime: only non-zero keys
	if stateMap, mapErr := g.ops.toMap(initial); mapErr == nil {
		if isMapStringAny[S]() {
			// For map state: all keys present in the map are considered available.
			for k := range stateMap {
				initialKeys[k] = true
				initialReadyKeys[k] = true
			}
		} else {
			// For struct state: all keys exist for validation purposes,
			// but only non-zero values are considered ready at runtime.
			for k, v := range stateMap {
				initialKeys[k] = true
				if isNonZero(v) {
					initialReadyKeys[k] = true
				}
			}
		}
	}

	// Validate data-flow declarations: cycles, satisfiability, uniqueness.
	if err := g.validateDataFlow(initialKeys); err != nil {
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
		graph:          g,
		state:          g.ops.copy(initial),
		ops:            g.ops,
		nodes:          g.nodes,
		completed:      make(map[string]bool),
		workQueue:      []string{g.entry},
		threadID:       cfg.threadID,
		extraEventHook: cfg.extraEventHook,
	}

	// Initialize data-flow scheduling fields.
	exec.dataflowMeta = make(map[string]DataFlowMeta, len(g.dataflow))
	for name, meta := range g.dataflow {
		exec.dataflowMeta[name] = meta
	}
	exec.readinessSet = make(map[string]bool)
	for k := range initialReadyKeys {
		exec.readinessSet[k] = true
	}
	exec.pending = make(map[string]bool)
	for name := range g.nodes {
		if name != g.entry {
			exec.pending[name] = true
		}
	}

	err := exec.execute(ctx)

	// Emit GraphCompleted event with final state, usage, and error (if any).
	// Both the graph-level hook and any per-run extra hook receive it.
	if g.eventHook != nil || cfg.extraEventHook != nil {
		snapshot, _ := g.ops.toMap(exec.state)
		completed := GraphEvent{
			Type:          EventGraphCompleted,
			Timestamp:     time.Now(),
			StateSnapshot: snapshot,
			Usage:         exec.usage,
			ThreadID:      cfg.threadID,
			Error:         err,
		}
		if g.eventHook != nil {
			g.eventHook.OnEvent(completed)
		}
		if cfg.extraEventHook != nil {
			cfg.extraEventHook.OnEvent(completed)
		}
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

// EventHook returns the graph's configured event hook, or nil if none is set.
func (g *Graph[S]) EventHook() GraphEventHook {
	return g.eventHook
}

// SetEventHook sets or replaces the graph's event hook. Pass nil to disable.
func (g *Graph[S]) SetEventHook(hook GraphEventHook) {
	g.eventHook = hook
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
