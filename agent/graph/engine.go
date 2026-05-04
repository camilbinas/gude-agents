package graph

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// runExec[S] holds all mutable state for a single Graph[S].Run call.
// It is parameterized by the state type S, using the stateOps[S] strategy
// interface for state manipulation. This allows the same engine logic to
// operate on both map[string]any (zero-cost) and custom struct types (JSON at boundaries).
type runExec[S any] struct {
	graph  *Graph[S]
	state  S
	ops    stateOps[S]
	mu     sync.Mutex
	usage  agent.TokenUsage
	nodes  map[string]NodeFunc[S] // generic node functions
	routes map[string]route[S]    // generic routing rules

	completed  map[string]bool
	iterations int
	// isBranch is true for isolated branch execs created inside forkStep.
	// Branch execs skip the join barrier check because their completed map is
	// partial — the parent exec fires joins after merging all branch results.
	isBranch bool

	// Iterative execution fields.
	workQueue          []string // nodes pending execution
	threadID           string   // for checkpointing
	skipFirstInterrupt bool     // when true, skip interrupt check for the first node (used by Resume)
}

// emitEvent sends a GraphEvent to the configured event hook, if any.
// This is a no-op when no event hook is configured (zero overhead).
// It uses ops.toMap to produce the state snapshot for the event; if serialization
// fails, the event is emitted with a nil StateSnapshot (non-fatal).
func (e *runExec[S]) emitEvent(event GraphEvent) {
	if e.graph.eventHook == nil {
		return
	}
	// If StateSnapshot is not already set, populate it from a copy of the current state.
	if event.StateSnapshot == nil {
		stateCopy := e.ops.copy(e.state)
		snapshot, err := e.ops.toMap(stateCopy)
		if err == nil {
			event.StateSnapshot = snapshot
		}
		// On error, emit with nil StateSnapshot (non-fatal).
	}
	e.graph.eventHook.OnEvent(event)
}

// execute runs the iterative work queue loop until the queue is empty,
// an error occurs, or the context is cancelled.
// This is the generic version that operates on any state type S via stateOps[S].
func (e *runExec[S]) execute(ctx context.Context) error {
	// Emit GraphStarted event at the beginning of execution.
	e.emitEvent(GraphEvent{
		Type:      EventGraphStarted,
		Timestamp: time.Now(),
		ThreadID:  e.threadID,
	})

	for len(e.workQueue) > 0 {
		// Check context cancellation.
		if err := ctx.Err(); err != nil {
			return err
		}

		// Pop the next node from the front of the queue.
		nodeName := e.workQueue[0]
		e.workQueue = e.workQueue[1:]

		// Check InterruptBefore: pause before executing the node.
		if e.graph.interruptBefore[nodeName] && e.graph.checkpointer != nil && !e.skipFirstInterrupt {
			saved, err := e.saveCheckpoint(ctx, nodeName)
			if err != nil {
				return err
			}
			// Trace the interrupt.
			if e.graph.tracingHook != nil {
				e.graph.tracingHook.OnInterrupt(ctx, nodeName, InterruptTypeBefore, saved.Version)
			}
			// Emit InterruptFired event for InterruptBefore.
			e.emitEvent(GraphEvent{
				Type:          EventInterruptFired,
				Timestamp:     time.Now(),
				NodeName:      nodeName,
				InterruptType: InterruptTypeBefore,
				Version:       saved.Version,
				ThreadID:      e.threadID,
			})
			return &GraphInterruptError{
				Result: InterruptResult{
					NodeName:   nodeName,
					Type:       InterruptTypeBefore,
					Checkpoint: saved,
				},
			}
		}

		// Clear skipFirstInterrupt after the first node's interrupt check.
		e.skipFirstInterrupt = false

		// Check and increment iteration counter.
		e.mu.Lock()
		if e.iterations >= e.graph.maxIter {
			e.mu.Unlock()
			return &GraphIterationError{Limit: e.graph.maxIter}
		}
		e.iterations++
		e.mu.Unlock()

		fn := e.nodes[nodeName]

		// Execute node with a copy of current state.
		e.mu.Lock()
		stateCopy := e.ops.copy(e.state)
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

		// Emit NodeStarted event before executing the node function.
		e.emitEvent(GraphEvent{
			Type:      EventNodeStarted,
			Timestamp: time.Now(),
			NodeName:  nodeName,
			ThreadID:  e.threadID,
		})

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

		// Extract usage and merge result into shared state.
		e.mu.Lock()
		e.extractUsage(&result)
		e.ops.merge(&e.state, result)
		e.completed[nodeName] = true
		e.mu.Unlock()

		// Emit NodeCompleted event after node execution and state merge.
		e.emitEvent(GraphEvent{
			Type:      EventNodeCompleted,
			Timestamp: time.Now(),
			NodeName:  nodeName,
			Usage:     e.usage,
			ThreadID:  e.threadID,
		})

		// Check InterruptAfter: pause after executing the node.
		if e.graph.interruptAfter[nodeName] && e.graph.checkpointer != nil {
			saved, err := e.saveCheckpoint(ctx, nodeName)
			if err != nil {
				return err
			}
			// Trace the interrupt.
			if e.graph.tracingHook != nil {
				e.graph.tracingHook.OnInterrupt(ctx, nodeName, InterruptTypeAfter, saved.Version)
			}
			// Emit InterruptFired event for InterruptAfter.
			e.emitEvent(GraphEvent{
				Type:          EventInterruptFired,
				Timestamp:     time.Now(),
				NodeName:      nodeName,
				InterruptType: InterruptTypeAfter,
				Version:       saved.Version,
				ThreadID:      e.threadID,
			})
			return &GraphInterruptError{
				Result: InterruptResult{
					NodeName:   nodeName,
					Type:       InterruptTypeAfter,
					Checkpoint: saved,
				},
			}
		}

		// Save checkpoint after node completion (unless checkpointOnInterruptOnly).
		if e.graph.checkpointer != nil && !e.graph.checkpointOnInterruptOnly {
			saved, err := e.saveCheckpoint(ctx, nodeName)
			if err != nil {
				return err
			}
			// Emit CheckpointSaved event after successful checkpoint save.
			e.emitEvent(GraphEvent{
				Type:      EventCheckpointSaved,
				Timestamp: time.Now(),
				NodeName:  nodeName,
				Version:   saved.Version,
				ThreadID:  e.threadID,
			})
		}

		// Dispatch next node(s) to work queue via routing rules.
		if err := e.dispatchNext(ctx, nodeName); err != nil {
			return err
		}

		// Check join barrier only when NOT inside a fork branch.
		if !e.isBranch {
			e.checkJoins(nodeName)
		}
	}
	return nil
}

// extractUsage extracts token usage from the node result.
// For struct types implementing usageCarrier (via GraphState embedding), it uses
// the interface methods. For map types, it checks the __usage__ key directly.
func (e *runExec[S]) extractUsage(result *S) {
	// Try usageCarrier interface first (for struct types embedding GraphState).
	if carrier, ok := any(result).(usageCarrier); ok {
		if u := carrier.getPendingUsage(); u.InputTokens > 0 || u.OutputTokens > 0 {
			e.usage.InputTokens += u.InputTokens
			e.usage.OutputTokens += u.OutputTokens
			carrier.clearPendingUsage()
		}
		return
	}

	// For map types, check the __usage__ key directly.
	if m, ok := any(result).(*map[string]any); ok {
		if u, exists := (*m)["__usage__"].(agent.TokenUsage); exists {
			e.usage.InputTokens += u.InputTokens
			e.usage.OutputTokens += u.OutputTokens
			delete(*m, "__usage__")
		}
	}
}

// dispatchNext enqueues the next node(s) based on routing rules for the given node.
// For fork routes, it runs branches in parallel and then checks joins.
func (e *runExec[S]) dispatchNext(ctx context.Context, nodeName string) error {
	r, hasRoute := e.routes[nodeName]
	if !hasRoute {
		return nil
	}

	switch {
	case r.static != "":
		e.workQueue = append(e.workQueue, r.static)
	case r.conditional != nil:
		e.mu.Lock()
		currentState := e.ops.copy(e.state)
		e.mu.Unlock()
		next, err := r.conditional(ctx, currentState)
		if err != nil {
			return fmt.Errorf("graph: conditional router for %q: %w", nodeName, err)
		}
		if next != "" {
			if _, ok := e.nodes[next]; !ok {
				return &GraphValidationError{Message: fmt.Sprintf("router returned unknown node %q", next)}
			}
			e.workQueue = append(e.workQueue, next)
		}
	case len(r.fork) > 0:
		if err := e.forkStep(ctx, r.fork); err != nil {
			return err
		}
	}

	return nil
}

// checkJoins enqueues any join node whose all predecessors are now complete.
func (e *runExec[S]) checkJoins(nodeName string) {
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
			e.workQueue = append(e.workQueue, joinNode)
		}
	}
}

// saveCheckpoint serializes state via ops.toMap and persists a checkpoint
// via the configured checkpointer. Returns the saved checkpoint (with version
// assigned by the checkpointer) or an error.
func (e *runExec[S]) saveCheckpoint(ctx context.Context, nodeName string) (Checkpoint, error) {
	e.mu.Lock()
	stateCopy := e.ops.copy(e.state)
	completedCopy := copyCompleted(e.completed)
	iterations := e.iterations
	usage := e.usage
	e.mu.Unlock()

	stateMap, err := e.ops.toMap(stateCopy)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("graph: node %q: failed to serialize state for checkpoint: %w", nodeName, err)
	}

	if err := validateStateSerializable(stateMap); err != nil {
		return Checkpoint{}, err
	}

	cp := Checkpoint{
		ThreadID:   e.threadID,
		State:      stateMap,
		Completed:  completedCopy,
		Iterations: iterations,
		Usage:      usage,
		NodeName:   nodeName,
		Timestamp:  time.Now(),
	}

	// Start checkpoint tracing span.
	var finishCheckpointTrace func(err error)
	if e.graph.tracingHook != nil {
		finishCheckpointTrace = e.graph.tracingHook.OnCheckpointSave(ctx, nodeName, 0)
	}

	saved, err := e.graph.checkpointer.Save(ctx, e.threadID, cp)

	if finishCheckpointTrace != nil {
		finishCheckpointTrace(err)
	}
	if err != nil {
		return Checkpoint{}, err
	}
	return saved, nil
}

// forkStep executes multiple nodes concurrently and merges their states in sorted order.
// Each branch gets an isolated copy of the snapshot state and its own runExec[S].
// Results are merged in alphabetical order using ops.mergeDiff for deterministic output.
// If any branch returns an error, the context is cancelled and the first error is returned.
func (e *runExec[S]) forkStep(ctx context.Context, targets []string) error {
	forkCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Snapshot current state for all branches.
	e.mu.Lock()
	snapshot := e.ops.copy(e.state)
	e.mu.Unlock()

	// Sort targets for deterministic merge order.
	sorted := make([]string, len(targets))
	copy(sorted, targets)
	sort.Strings(sorted)

	type branchResult struct {
		state  S
		branch *runExec[S]
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

			branch := &runExec[S]{
				graph:     e.graph,
				state:     e.ops.copy(snapshot),
				ops:       e.ops,
				nodes:     e.nodes,
				routes:    e.routes,
				completed: completedCopy,
				isBranch:  true,
				workQueue: []string{name},
			}

			err := branch.execute(forkCtx)

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

	// Merge branch results into parent state in sorted order.
	// Use mergeDiff to only apply keys that changed from the snapshot,
	// preventing branches from overwriting each other's results.
	e.mu.Lock()
	for _, br := range branchResults {
		if br.err != nil {
			continue
		}
		e.ops.mergeDiff(&e.state, snapshot, br.state)
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
		e.checkJoins(name)
	}

	return nil
}
