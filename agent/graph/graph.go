package graph

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// State is the shared data container passed between nodes.
type State map[string]any

// NodeFunc is the unit of work executed by a node.
type NodeFunc func(ctx context.Context, state State) (State, error)

// RouterFunc decides the next node at runtime.
// Returning "" signals end-of-graph.
type RouterFunc func(ctx context.Context, state State) (string, error)

// GraphResult is returned by Graph.Run on success.
type GraphResult struct {
	State State
	Usage agent.TokenUsage
}

// route is a sealed union: exactly one field is set.
type route struct {
	static      string     // static edge target
	conditional RouterFunc // conditional edge router
	fork        []string   // fork targets
}

// Graph is a directed graph of named nodes connected by routing rules.
// Fields are written only during construction; after that they are read-only,
// making concurrent Run calls safe.
type Graph struct {
	nodes       map[string]NodeFunc
	entry       string
	routes      map[string]route    // one route per source node
	joins       map[string][]string // node → required predecessors
	maxIter     int
	tracingHook GraphTracingHook // nil = no tracing
	metricsHook GraphMetricsHook // nil = no metrics
	loggingHook GraphLoggingHook // nil = no structured logging
}

// GraphOption configures a Graph.
type GraphOption func(*Graph) error

// WithMaxIterations sets the maximum number of node executions per Run.
// Returns an error if n < 1.
func WithMaxIterations(n int) GraphOption {
	return func(g *Graph) error {
		if n < 1 {
			return &GraphValidationError{Message: "MaxIterations must be >= 1"}
		}
		g.maxIter = n
		return nil
	}
}

// NewGraph creates an empty, unconfigured Graph with default maxIter of 100.
func NewGraph(opts ...GraphOption) (*Graph, error) {
	g := &Graph{
		nodes:   make(map[string]NodeFunc),
		routes:  make(map[string]route),
		joins:   make(map[string][]string),
		maxIter: 100,
	}
	for _, opt := range opts {
		if err := opt(g); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// AddNode registers a named node. Returns an error on empty name, nil fn, or duplicate name.
func (g *Graph) AddNode(name string, fn NodeFunc) error {
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
func (g *Graph) SetEntry(name string) {
	g.entry = name
}

// AddEdge registers a static edge from → to. Returns an error on empty from or to.
func (g *Graph) AddEdge(from, to string) error {
	if from == "" {
		return &GraphValidationError{Message: "AddEdge: from must not be empty"}
	}
	if to == "" {
		return &GraphValidationError{Message: "AddEdge: to must not be empty"}
	}
	g.routes[from] = route{static: to}
	return nil
}

// AddConditionalEdge registers a conditional edge from the given node.
func (g *Graph) AddConditionalEdge(from string, router RouterFunc) error {
	if from == "" {
		return &GraphValidationError{Message: "AddConditionalEdge: from must not be empty"}
	}
	if router == nil {
		return &GraphValidationError{Message: fmt.Sprintf("AddConditionalEdge: router for %q must not be nil", from)}
	}
	g.routes[from] = route{conditional: router}
	return nil
}

// AddFork registers a parallel fork from one node to multiple targets.
// Returns an error if fewer than 2 targets are provided.
func (g *Graph) AddFork(from string, targets []string) error {
	if from == "" {
		return &GraphValidationError{Message: "AddFork: from must not be empty"}
	}
	if len(targets) < 2 {
		return &GraphValidationError{Message: fmt.Sprintf("AddFork: node %q requires at least 2 targets", from)}
	}
	g.routes[from] = route{fork: targets}
	return nil
}

// AddJoin registers a join barrier: node waits for all predecessors.
// Returns an error if fewer than 2 predecessors are provided.
func (g *Graph) AddJoin(node string, predecessors []string) error {
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
func (g *Graph) validate() error {
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

// runExec holds all mutable state for a single Graph.Run call.
type runExec struct {
	graph      *Graph
	state      State
	mu         sync.Mutex
	usage      agent.TokenUsage
	completed  map[string]bool
	iterations int
	// isBranch is true for isolated branch execs created inside forkStep.
	// Branch execs skip the join barrier check because their completed map is
	// partial — the parent exec fires joins after merging all branch results.
	isBranch bool
}

// step executes a single node and dispatches to the next node(s).
func (e *runExec) step(ctx context.Context, nodeName string) error {
	// Check context cancellation.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Check and increment iteration counter.
	e.mu.Lock()
	if e.iterations >= e.graph.maxIter {
		e.mu.Unlock()
		return &GraphIterationError{Limit: e.graph.maxIter}
	}
	e.iterations++
	e.mu.Unlock()

	fn := e.graph.nodes[nodeName]

	// Execute node with a copy of current state.
	e.mu.Lock()
	stateCopy := CopyState(e.state)
	e.mu.Unlock()

	// Start node tracing span if hook is set.
	var finishNode func(err error)
	if e.graph.tracingHook != nil {
		ctx, finishNode = e.graph.tracingHook.OnNodeStart(ctx, nodeName)
	}

	// Start node metrics tracking if metrics hook is set.
	var finishNodeMetrics func(err error)
	if e.graph.metricsHook != nil {
		finishNodeMetrics = e.graph.metricsHook.OnNodeStart(nodeName)
	}

	// Start node logging if logging hook is set.
	var nodeStart time.Time
	if e.graph.loggingHook != nil {
		e.graph.loggingHook.OnNodeStart(nodeName)
		nodeStart = time.Now()
	}

	result, err := fn(ctx, stateCopy)

	if finishNode != nil {
		finishNode(err)
	}
	if finishNodeMetrics != nil {
		finishNodeMetrics(err)
	}
	if e.graph.loggingHook != nil {
		e.graph.loggingHook.OnNodeEnd(nodeName, err, time.Since(nodeStart))
	}

	if err != nil {
		return err
	}

	// Extract __usage__ key and accumulate, then merge result into shared state.
	e.mu.Lock()
	if u, ok := result["__usage__"].(agent.TokenUsage); ok {
		e.usage.InputTokens += u.InputTokens
		e.usage.OutputTokens += u.OutputTokens
		delete(result, "__usage__")
	}
	mergeState(e.state, result)
	e.completed[nodeName] = true
	e.mu.Unlock()

	// Dispatch to next node(s) via routing rule.
	r, hasRoute := e.graph.routes[nodeName]
	if hasRoute {
		switch {
		case r.static != "":
			if err := e.step(ctx, r.static); err != nil {
				return err
			}
		case r.conditional != nil:
			e.mu.Lock()
			currentState := CopyState(e.state)
			e.mu.Unlock()
			next, err := r.conditional(ctx, currentState)
			if err != nil {
				return fmt.Errorf("graph: conditional router for %q: %w", nodeName, err)
			}
			if next != "" {
				if _, ok := e.graph.nodes[next]; !ok {
					return &GraphValidationError{Message: fmt.Sprintf("router returned unknown node %q", next)}
				}
				if err := e.step(ctx, next); err != nil {
					return err
				}
			}
		case len(r.fork) > 0:
			if err := e.forkStep(ctx, r.fork); err != nil {
				return err
			}
		}
	}

	// Check join barrier only when NOT inside a fork branch.
	if !e.isBranch {
		if err := e.checkJoins(ctx, nodeName); err != nil {
			return err
		}
	}

	return nil
}

// checkJoins fires any join node whose all predecessors are now complete.
func (e *runExec) checkJoins(ctx context.Context, nodeName string) error {
	for joinNode, preds := range e.graph.joins {
		isPred := false
		for _, p := range preds {
			if p == nodeName {
				isPred = true
				break
			}
		}
		if !isPred {
			continue
		}
		e.mu.Lock()
		allDone := true
		alreadyFired := e.completed[joinNode]
		for _, p := range preds {
			if !e.completed[p] {
				allDone = false
				break
			}
		}
		e.mu.Unlock()
		if allDone && !alreadyFired {
			if err := e.step(ctx, joinNode); err != nil {
				return err
			}
		}
	}
	return nil
}

// forkStep executes multiple nodes concurrently and merges their states in sorted order.
func (e *runExec) forkStep(ctx context.Context, targets []string) error {
	forkCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Snapshot current state for all branches.
	e.mu.Lock()
	snapshot := CopyState(e.state)
	e.mu.Unlock()

	// Sort targets for deterministic merge order.
	sorted := make([]string, len(targets))
	copy(sorted, targets)
	sort.Strings(sorted)

	type branchResult struct {
		state  State
		branch *runExec
		err    error
	}
	branchResults := make([]branchResult, len(sorted))

	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for i, target := range sorted {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()

			// Each branch gets its own runExec with a copy of the snapshot state.
			e.mu.Lock()
			completedCopy := make(map[string]bool, len(e.completed))
			for k, v := range e.completed {
				completedCopy[k] = v
			}
			e.mu.Unlock()

			branch := &runExec{
				graph:     e.graph,
				state:     CopyState(snapshot),
				completed: completedCopy,
				isBranch:  true,
			}

			err := branch.step(forkCtx, name)

			errMu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
				cancel()
			}
			errMu.Unlock()

			branchResults[idx] = branchResult{state: branch.state, branch: branch, err: err}
		}(i, target)
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	// Merge branch metadata (completed, usage, iterations) and states into parent.
	e.mu.Lock()
	for _, br := range branchResults {
		if br.err != nil {
			continue
		}
		mergeState(e.state, br.state)
		for k, v := range br.branch.completed {
			e.completed[k] = v
		}
		e.usage.InputTokens += br.branch.usage.InputTokens
		e.usage.OutputTokens += br.branch.usage.OutputTokens
		e.iterations += br.branch.iterations
	}
	e.mu.Unlock()

	// Now that all branch completions are merged into the parent's completed map,
	// check join barriers for each branch target.
	for _, name := range sorted {
		if err := e.checkJoins(ctx, name); err != nil {
			return err
		}
	}

	return nil
}

// Run validates the graph and executes it from the entry node.
func (g *Graph) Run(ctx context.Context, initial State) (GraphResult, error) {
	if err := g.validate(); err != nil {
		return GraphResult{}, err
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

	exec := &runExec{
		graph:     g,
		state:     CopyState(initial),
		completed: make(map[string]bool),
	}

	err := exec.step(ctx, g.entry)

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
		return GraphResult{}, err
	}

	return GraphResult{
		State: exec.state,
		Usage: exec.usage,
	}, nil
}
