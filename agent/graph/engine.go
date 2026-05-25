package graph

import (
	"context"
	"fmt"
	"reflect"
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
	graph *Graph[S]
	state S
	ops   stateOps[S]
	mu    sync.Mutex
	usage agent.TokenUsage
	nodes map[string]NodeFunc[S] // generic node functions

	completed  map[string]bool
	iterations int

	// Iterative execution fields.
	workQueue          []string // nodes pending execution
	threadID           string   // for checkpointing
	skipFirstInterrupt bool     // when true, skip interrupt check for the first node (used by Resume)

	// Data-flow scheduling fields.
	readinessSet map[string]bool         // keys that have been produced
	dataflowMeta map[string]DataFlowMeta // copied from graph at init
	pending      map[string]bool         // nodes not yet executed (excludes entry)

	// extraEventHook is a per-run hook injected via runConfig (e.g. by
	// RunEventStream). It receives events alongside graph.eventHook without
	// requiring mutation of the graph. Nil means no extra hook.
	extraEventHook GraphEventHook
}

// emitEvent sends a GraphEvent to the configured event hooks, if any.
// Both the graph-level eventHook and any per-run extraEventHook receive the
// event. The state snapshot is populated lazily from the current state when
// not already set on the event.
func (e *runExec[S]) emitEvent(event GraphEvent) {
	if e.graph.eventHook == nil && e.extraEventHook == nil {
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
	if e.graph.eventHook != nil {
		e.graph.eventHook.OnEvent(event)
	}
	if e.extraEventHook != nil {
		e.extraEventHook.OnEvent(event)
	}
}

// execute runs the iterative work queue loop until the queue is empty,
// an error occurs, or the context is cancelled.
// This is the generic version that operates on any state type S via stateOps[S].
//
// Execution flow:
// 1. Execute entry node from workQueue (existing logic)
// 2. After entry completes, call updateReadiness(entry)
// 3. Enter data-flow scheduling loop:
//   - scheduleReady() → if 1 node, execute sequentially; if N>1, executeConcurrent; if 0, terminate
func (e *runExec[S]) execute(ctx context.Context) error {
	// Stash the effective hook (graph-level + per-call) on the context so
	// agent nodes can pick up per-call hooks like RunEventStream's channel
	// without having to mutate g.eventHook for the duration of the call.
	ctx = withEffectiveHook(ctx, effectiveEventHook{
		graphHook: e.graph.eventHook,
		extraHook: e.extraEventHook,
	})

	// Emit GraphStarted event at the beginning of execution.
	e.emitEvent(GraphEvent{
		Type:      EventGraphStarted,
		Timestamp: time.Now(),
		ThreadID:  e.threadID,
	})

	// Phase 1: Execute entry node from workQueue.
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

		// Execute the node using the shared helper (handles iteration check,
		// state copy, hooks, event emission, fn call, usage extraction).
		result, err := e.executeNode(ctx, nodeName)
		if err != nil {
			return err
		}

		// Merge result into shared state and mark completed.
		e.mu.Lock()
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

		// After entry node completes, update readiness and enter scheduling loop.
		e.updateReadiness(nodeName)
	}

	// Phase 2: Data-flow scheduling loop.
	// Schedule nodes based on readiness (all input keys present in readinessSet).
	for {
		// Check context cancellation.
		if err := ctx.Err(); err != nil {
			return err
		}

		ready := e.scheduleReady()
		if len(ready) == 0 {
			// No more nodes can become ready — terminate.
			break
		}

		// Check if any ready node has InterruptBefore configured.
		// If so, execute nodes sequentially to respect interrupt semantics.
		hasInterrupt := false
		for _, name := range ready {
			if e.graph.interruptBefore[name] {
				hasInterrupt = true
				break
			}
		}

		if len(ready) == 1 || hasInterrupt {
			// Execute single node sequentially (or first node if interrupt forces sequential).
			nodeName := ready[0]
			if err := e.executeSequentialNode(ctx, nodeName); err != nil {
				return err
			}
		} else {
			// Execute multiple nodes concurrently.
			if err := e.executeConcurrent(ctx, ready); err != nil {
				return err
			}
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

// saveCheckpoint serializes state via ops.toMap and persists a checkpoint
// via the configured checkpointer. Returns the saved checkpoint (with version
// assigned by the checkpointer) or an error.
func (e *runExec[S]) saveCheckpoint(ctx context.Context, nodeName string) (Checkpoint, error) {
	e.mu.Lock()
	stateCopy := e.ops.copy(e.state)
	completedCopy := copyCompleted(e.completed)
	iterations := e.iterations
	usage := e.usage
	readinessCopy := copyCompleted(e.readinessSet)
	e.mu.Unlock()

	stateMap, err := e.ops.toMap(stateCopy)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("graph: node %q: failed to serialize state for checkpoint: %w", nodeName, err)
	}

	if err := validateStateSerializable(stateMap); err != nil {
		return Checkpoint{}, err
	}

	cp := Checkpoint{
		ThreadID:     e.threadID,
		State:        stateMap,
		Completed:    completedCopy,
		ReadinessSet: readinessCopy,
		Iterations:   iterations,
		Usage:        usage,
		NodeName:     nodeName,
		Timestamp:    time.Now(),
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

// findReadyNodes is a standalone scheduling function that iterates pending nodes
// and returns a sorted list of node names whose declared input keys are all
// present in the readinessSet. Nodes with empty input keys are immediately ready.
// The returned list is sorted alphabetically for determinism.
//
// This is the single source of truth for scheduling logic, used by both
// runExec.scheduleReady() and Graph.Step().
func findReadyNodes(pending map[string]bool, dataflowMeta map[string]DataFlowMeta, readinessSet map[string]bool) []string {
	var ready []string
	for nodeName := range pending {
		meta := dataflowMeta[nodeName]
		allReady := true
		for _, key := range meta.InputKeys {
			if !readinessSet[key] {
				allReady = false
				break
			}
		}
		if allReady {
			ready = append(ready, nodeName)
		}
	}
	sort.Strings(ready)
	return ready
}

// scheduleReady iterates pending nodes and returns a sorted list of node names
// whose declared input keys are all present in the readinessSet.
// Nodes with empty input keys are immediately ready.
// The returned list is sorted alphabetically for determinism.
func (e *runExec[S]) scheduleReady() []string {
	return findReadyNodes(e.pending, e.dataflowMeta, e.readinessSet)
}

// updateReadiness adds the node's declared output keys to the readinessSet
// and removes the node from pending.
//
// For map-based state: uses hasKey for direct map lookup (zero cost).
// Synthetic scheduling keys (prefixed with "__connect:") are added unconditionally
// since they represent ordering constraints, not actual state data.
// For struct-based state: uses hasKey for targeted reflection per output key,
// avoiding the full json.Marshal that toMap would perform. If hasKey returns
// false for a key that doesn't correspond to a struct field (abstract
// scheduling key), the key is added unconditionally since the node completed.
// If hasKey returns false for a key that IS a struct field, the field has a
// zero value and readiness is NOT granted (conditional gating).
func (e *runExec[S]) updateReadiness(nodeName string) {
	meta := e.dataflowMeta[nodeName]
	for _, key := range meta.OutputKeys {
		if isSchedulingKey(key) {
			// Synthetic scheduling keys are always satisfied when the node completes.
			e.readinessSet[key] = true
			continue
		}
		if isMapStringAny[S]() {
			// For map state: hasKey does a direct map lookup (zero cost).
			if e.ops.hasKey(e.state, key) {
				e.readinessSet[key] = true
			}
		} else {
			// For struct state: hasKey uses targeted reflection on the specific
			// struct field. Returns true if the field has a non-zero value.
			// Returns false if either:
			//   (a) the key doesn't map to a struct field (abstract scheduling key), or
			//   (b) the field exists but has a zero value.
			// We distinguish these cases using jsonFieldIndex.
			if e.ops.hasKey(e.state, key) {
				e.readinessSet[key] = true
			} else {
				// Check if this key corresponds to a known struct field.
				// If not, it's an abstract scheduling key — add unconditionally.
				if _, isStructField := jsonFieldIndex[S](key); !isStructField {
					e.readinessSet[key] = true
				}
				// If it IS a struct field but hasKey returned false, the field
				// is zero — do NOT add to readiness (conditional gating).
			}
		}
	}
	delete(e.pending, nodeName)
}

// isSchedulingKey reports whether a key is a synthetic scheduling key
// generated by Connect(). These keys exist purely for ordering and are
// never written to state.
func isSchedulingKey(key string) bool {
	return len(key) > 10 && key[:10] == "__connect:"
}

// isNonZero reports whether a value is non-zero for readiness checking.
// Values in this path come from a JSON round-trip via ops.toMap, so the input
// types are limited to: nil, bool, float64, string, []any, map[string]any.
// reflect.Value.IsZero handles all of them uniformly. For non-nil composite
// types (slices, maps), IsZero returns true only when the value is nil — an
// empty but non-nil slice/map is treated as present, matching the behaviour
// that "the field was set, just to its empty value".
func isNonZero(val any) bool {
	if val == nil {
		return false
	}
	return !reflect.ValueOf(val).IsZero()
}

// executeNode encapsulates the common node execution logic shared across all
// execution paths (entry-loop, executeSequentialNode, executeConcurrent goroutine).
// It performs: iteration increment with max check, state copy, tracing/metrics/logging
// hook start, NodeStarted event emission, node function invocation, hook finish calls,
// and usage extraction.
//
// The caller is responsible for: merge into shared state, checkpoint saves,
// readiness updates, and interrupt checks (which differ per execution mode).
//
// Returns the result state from the node function, or an error if the node
// function fails or the iteration limit is exceeded.
func (e *runExec[S]) executeNode(ctx context.Context, nodeName string) (S, error) {
	var zero S

	// 1. Increment iteration count and check max iterations.
	e.mu.Lock()
	if e.iterations >= e.graph.maxIter {
		e.mu.Unlock()
		return zero, &GraphIterationError{Limit: e.graph.maxIter}
	}
	e.iterations++
	e.mu.Unlock()

	// 2. Copy the current state for isolation.
	e.mu.Lock()
	stateCopy := e.ops.copy(e.state)
	e.mu.Unlock()

	// 3. Start tracing/metrics/logging hooks for the node.
	nodeCtx := ctx
	var finishNode func(err error)
	if e.graph.tracingHook != nil {
		nodeCtx, finishNode = e.graph.tracingHook.OnNodeStart(ctx, nodeName)
	}

	var finishNodeMetrics func(err error)
	if e.graph.metricsHook != nil {
		finishNodeMetrics = e.graph.metricsHook.OnNodeStart(nodeName)
	}

	var nodeStart time.Time
	if e.graph.loggingHook != nil {
		e.graph.loggingHook.OnNodeStart(nodeName)
		nodeStart = time.Now()
	}

	// 4. Emit NodeStarted event.
	e.emitEvent(GraphEvent{
		Type:      EventNodeStarted,
		Timestamp: time.Now(),
		NodeName:  nodeName,
		ThreadID:  e.threadID,
	})

	// 5. Call the node function with the copied state.
	fn := e.nodes[nodeName]
	result, err := fn(nodeCtx, stateCopy)

	// 6. Call hook finish functions.
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
		return zero, err
	}

	// 7. Extract usage from agent context if applicable.
	e.mu.Lock()
	e.extractUsage(&result)
	e.mu.Unlock()

	// 8. Return the result state.
	return result, nil
}

// executeSequentialNode executes a single data-flow node sequentially.
// It delegates core execution to executeNode and handles interrupt checks,
// checkpoint saves, and readiness updates in the caller.
func (e *runExec[S]) executeSequentialNode(ctx context.Context, nodeName string) error {
	// Check InterruptBefore: pause before executing the node.
	if e.graph.interruptBefore[nodeName] && e.graph.checkpointer != nil {
		saved, err := e.saveCheckpoint(ctx, nodeName)
		if err != nil {
			return err
		}
		if e.graph.tracingHook != nil {
			e.graph.tracingHook.OnInterrupt(ctx, nodeName, InterruptTypeBefore, saved.Version)
		}
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

	// Execute the node using the shared helper (handles iteration check,
	// state copy, hooks, event emission, fn call, usage extraction).
	result, err := e.executeNode(ctx, nodeName)
	if err != nil {
		return err
	}

	// Merge result into shared state and mark completed.
	e.mu.Lock()
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
		if e.graph.tracingHook != nil {
			e.graph.tracingHook.OnInterrupt(ctx, nodeName, InterruptTypeAfter, saved.Version)
		}
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
		e.emitEvent(GraphEvent{
			Type:      EventCheckpointSaved,
			Timestamp: time.Now(),
			NodeName:  nodeName,
			Version:   saved.Version,
			ThreadID:  e.threadID,
		})
	}

	// Update readiness after successful execution.
	e.updateReadiness(nodeName)

	return nil
}

// executeConcurrent executes multiple data-flow nodes concurrently.
// Each node delegates to executeNode for core execution logic.
// Results are merged in sorted node-name order using ops.mergeDiff.
// On first error, the context is cancelled and the error is returned.
func (e *runExec[S]) executeConcurrent(ctx context.Context, nodes []string) error {
	// Sort nodes alphabetically for deterministic merge order.
	sort.Strings(nodes)

	// Create a cancellable context for concurrent execution.
	concCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Take a snapshot of the current state before concurrent execution.
	e.mu.Lock()
	snapshot := e.ops.copy(e.state)
	e.mu.Unlock()

	type nodeResult struct {
		name   string
		result S
		err    error
	}

	results := make([]nodeResult, len(nodes))
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	for i, nodeName := range nodes {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()

			// Execute the node using the shared helper (handles iteration check,
			// state copy, hooks, event emission, fn call, usage extraction).
			result, err := e.executeNode(concCtx, name)
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
					cancel() // Cancel other goroutines on first error.
				})
				results[idx] = nodeResult{name: name, err: err}
				return
			}

			results[idx] = nodeResult{name: name, result: result}
		}(i, nodeName)
	}

	wg.Wait()

	// If any node errored, return the first error.
	if firstErr != nil {
		return firstErr
	}

	// Merge results in sorted node-name order using mergeDiff.
	e.mu.Lock()
	for _, nr := range results {
		e.ops.mergeDiff(&e.state, snapshot, nr.result)
		e.completed[nr.name] = true
	}
	e.mu.Unlock()

	// Emit NodeCompleted events for each concurrent node (in sorted order).
	for _, nr := range results {
		e.emitEvent(GraphEvent{
			Type:      EventNodeCompleted,
			Timestamp: time.Now(),
			NodeName:  nr.name,
			Usage:     e.usage,
			ThreadID:  e.threadID,
		})
	}

	// Update readiness for all completed nodes.
	for _, nr := range results {
		e.updateReadiness(nr.name)
	}

	// Save per-node checkpoints after concurrent execution (unless checkpointOnInterruptOnly).
	// Each node gets its own checkpoint with cumulative Completed map up to that point,
	// matching the sequential checkpoint semantics where each node's completion is individually recorded.
	if e.graph.checkpointer != nil && !e.graph.checkpointOnInterruptOnly {
		for _, nr := range results {
			saved, err := e.saveCheckpoint(ctx, nr.name)
			if err != nil {
				return err
			}
			e.emitEvent(GraphEvent{
				Type:      EventCheckpointSaved,
				Timestamp: time.Now(),
				NodeName:  nr.name,
				Version:   saved.Version,
				ThreadID:  e.threadID,
			})
		}
	}

	return nil
}
