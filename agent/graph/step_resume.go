package graph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// Step executes the next single node and saves a checkpoint.
// If no checkpoint exists for threadID, executes the entry node.
// initialState is only used on the first Step call for a thread.
// Note: Step currently only works with Graph[State] (map[string]any).
func (g *Graph[S]) Step(ctx context.Context, initialState S, threadID string) (StepResult[S], error) {
	if g.checkpointer == nil {
		return StepResult[S]{}, ErrNoCheckpointer
	}
	if threadID == "" {
		return StepResult[S]{}, ErrThreadIDRequired
	}

	// Try to load existing checkpoint.
	cp, err := g.checkpointer.Load(ctx, threadID)
	if errors.Is(err, ErrCheckpointNotFound) {
		// First step: validate graph, execute entry node.
		if err := g.validate(); err != nil {
			return StepResult[S]{}, err
		}
		return g.executeOneNode(ctx, initialState, make(map[string]bool), 0, agent.TokenUsage{}, g.entry, threadID)
	}
	if err != nil {
		return StepResult[S]{}, err
	}

	// Subsequent calls: check if the checkpoint's node is completed.
	if !cp.Completed[cp.NodeName] {
		// InterruptBefore case: the node hasn't been executed yet, execute it now.
		state, err := g.ops.fromMap(cp.State)
		if err != nil {
			return StepResult[S]{}, fmt.Errorf("graph: Step: failed to deserialize checkpoint state: %w", err)
		}
		return g.executeOneNode(ctx, state, cp.Completed, cp.Iterations, cp.Usage, cp.NodeName, threadID)
	}

	// Node is completed — determine next node to execute using data-flow scheduling.
	state, err := g.ops.fromMap(cp.State)
	if err != nil {
		return StepResult[S]{}, fmt.Errorf("graph: Step: failed to deserialize checkpoint state: %w", err)
	}

	// Build readiness set from checkpoint's ReadinessSet (includes synthetic connect keys).
	// Fall back to state keys if ReadinessSet is not available (old checkpoints).
	readinessSet := make(map[string]bool)
	if cp.ReadinessSet != nil {
		for k, v := range cp.ReadinessSet {
			readinessSet[k] = v
		}
	} else {
		for k := range cp.State {
			readinessSet[k] = true
		}
	}
	// Also add output keys of completed nodes (covers synthetic keys from Then).
	for name := range cp.Completed {
		if meta, ok := g.dataflow[name]; ok {
			for _, key := range meta.OutputKeys {
				readinessSet[key] = true
			}
		}
	}

	// Build pending set: all nodes not yet completed (excluding entry).
	pending := make(map[string]bool)
	for name := range g.dataflow {
		if !cp.Completed[name] {
			pending[name] = true
		}
	}

	// Find the next ready node using the shared scheduling function (single source of truth).
	ready := findReadyNodes(pending, g.dataflow, readinessSet)

	var nextNode string
	if len(ready) > 0 {
		nextNode = ready[0] // Pick alphabetically first (list is already sorted).
	}

	if nextNode == "" {
		// No more nodes can become ready — graph is done.
		return StepResult[S]{State: state, NodeName: cp.NodeName, Version: cp.Version, Done: true, Usage: cp.Usage}, nil
	}

	// Execute the next ready node.
	return g.executeOneNode(ctx, state, cp.Completed, cp.Iterations, cp.Usage, nextNode, threadID)
}

// Resume continues execution from the latest checkpoint for threadID.
// Optional updates are merged into the checkpointed state before continuing.
// Returns Result[S] on completion, or GraphInterruptError if another interrupt is hit.
func (g *Graph[S]) Resume(ctx context.Context, threadID string, updates *S, opts ...RunOption) (Result[S], error) {
	if g.checkpointer == nil {
		return Result[S]{}, ErrNoCheckpointer
	}
	if threadID == "" {
		return Result[S]{}, ErrThreadIDRequired
	}

	// Parse run options (e.g. extraEventHook from ResumeEventStream).
	var cfg runConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	hooks := effectiveEventHook{graphHook: g.eventHook, extraHook: cfg.extraEventHook}

	// Start graph tracing span for the resumed execution.
	var finishTrace func(err error, iterations int)
	if g.tracingHook != nil {
		ctx, finishTrace = g.tracingHook.OnGraphRunStart(ctx)
	}

	cp, err := g.checkpointer.Load(ctx, threadID)
	if err != nil {
		if finishTrace != nil {
			finishTrace(err, 0)
		}
		return Result[S]{}, err
	}

	// Deserialize checkpoint state.
	resumeState, err := g.ops.fromMap(cp.State)
	if err != nil {
		if finishTrace != nil {
			finishTrace(err, 0)
		}
		return Result[S]{}, fmt.Errorf("graph: Resume: failed to deserialize checkpoint state: %w", err)
	}

	// Merge state updates into checkpoint state.
	if updates != nil {
		g.ops.merge(&resumeState, *updates)
	}

	// Emit Resumed event to graph-level + per-call hooks.
	if !hooks.isZero() {
		snapshot, _ := g.ops.toMap(resumeState)
		hooks.emit(GraphEvent{
			Type:          EventResumed,
			Timestamp:     time.Now(),
			Version:       cp.Version,
			StateSnapshot: snapshot,
			ThreadID:      threadID,
		})
	}

	// Trace the resume.
	if g.tracingHook != nil {
		g.tracingHook.OnResume(ctx, threadID, cp.Version)
	}

	// Determine next node to execute.
	var workQueue []string
	var skipFirst bool
	if !cp.Completed[cp.NodeName] {
		// InterruptBefore case: execute the interrupted node.
		workQueue = []string{cp.NodeName}
		skipFirst = true
	} else {
		// InterruptAfter or normal checkpoint: the node is completed.
		// Set empty workQueue — the data-flow scheduling loop will take over
		// after the workQueue phase completes.
		workQueue = nil
	}

	// Continue execution from next node using the generic engine.
	exec := &runExec[S]{
		graph:              g,
		state:              resumeState,
		ops:                g.ops,
		nodes:              g.nodes,
		completed:          copyCompleted(cp.Completed),
		iterations:         cp.Iterations,
		usage:              cp.Usage,
		workQueue:          workQueue,
		threadID:           threadID,
		skipFirstInterrupt: skipFirst,
		extraEventHook:     cfg.extraEventHook,
	}

	// Initialize data-flow scheduling fields for resume.
	exec.dataflowMeta = make(map[string]DataFlowMeta, len(g.dataflow))
	for name, meta := range g.dataflow {
		exec.dataflowMeta[name] = meta
	}
	// Initialize readinessSet from checkpoint's ReadinessSet if available,
	// otherwise compute from state keys and completed nodes' output keys.
	exec.readinessSet = make(map[string]bool)
	if cp.ReadinessSet != nil {
		for k, v := range cp.ReadinessSet {
			exec.readinessSet[k] = v
		}
		// Also add output keys of completed nodes that may not be in the saved
		// readiness set (e.g., InterruptAfter saves checkpoint before updateReadiness).
		for name := range cp.Completed {
			if meta, ok := g.dataflow[name]; ok {
				for _, key := range meta.OutputKeys {
					exec.readinessSet[key] = true
				}
			}
		}
	} else {
		// Fallback: compute from state keys.
		if stateMap, mapErr := g.ops.toMap(resumeState); mapErr == nil {
			for k := range stateMap {
				exec.readinessSet[k] = true
			}
		}
		// For typed (non-map) state, also add output keys of completed nodes unconditionally.
		if !isMapStringAny[S]() {
			for name := range cp.Completed {
				if meta, ok := g.dataflow[name]; ok {
					for _, key := range meta.OutputKeys {
						exec.readinessSet[key] = true
					}
				}
			}
		}
	}
	// Initialize pending with all nodes not yet completed (excluding entry).
	exec.pending = make(map[string]bool)
	for name := range g.nodes {
		if name != g.entry && !cp.Completed[name] {
			exec.pending[name] = true
		}
	}

	err = exec.execute(ctx)

	// Emit GraphCompleted event to both graph-level and per-call hooks.
	if !hooks.isZero() {
		snapshot, _ := g.ops.toMap(exec.state)
		hooks.emit(GraphEvent{
			Type:          EventGraphCompleted,
			Timestamp:     time.Now(),
			StateSnapshot: snapshot,
			Usage:         exec.usage,
			ThreadID:      threadID,
			Error:         err,
		})
	}

	if finishTrace != nil {
		finishTrace(err, exec.iterations)
	}

	if err != nil {
		return Result[S]{}, err
	}

	return Result[S]{State: exec.state, Usage: exec.usage}, nil
}

// RewindTo resets the execution position to the specified checkpoint version.
// Does not delete later checkpoints. Subsequent Resume/Step calls start from this version.
func (g *Graph[S]) RewindTo(ctx context.Context, threadID string, version int) error {
	if g.checkpointer == nil {
		return ErrNoCheckpointer
	}
	if threadID == "" {
		return ErrThreadIDRequired
	}

	// Verify the version exists.
	cp, err := g.checkpointer.LoadAt(ctx, threadID, version)
	if err != nil {
		return err
	}

	// Save a new checkpoint that copies the state from the target version.
	rewindCp := Checkpoint{
		ThreadID:     threadID,
		State:        CopyState(cp.State),
		Completed:    copyCompleted(cp.Completed),
		ReadinessSet: copyCompleted(cp.ReadinessSet),
		Iterations:   cp.Iterations,
		Usage:        cp.Usage,
		NodeName:     cp.NodeName,
		Timestamp:    time.Now(),
	}
	_, err = g.checkpointer.Save(ctx, threadID, rewindCp)
	if err != nil {
		return err
	}

	// Emit RewindCompleted event to both graph-level and per-call hooks.
	hooks := effectiveEventHook{graphHook: g.eventHook}
	if !hooks.isZero() {
		hooks.emit(GraphEvent{
			Type:          EventRewindCompleted,
			Timestamp:     time.Now(),
			Version:       version,
			StateSnapshot: CopyState(cp.State),
			ThreadID:      threadID,
		})
	}

	// Trace the rewind.
	if g.tracingHook != nil {
		g.tracingHook.OnRewind(ctx, threadID, version)
	}

	return nil
}

// executeOneNode executes a single node, saves a checkpoint, and returns a StepResult.
func (g *Graph[S]) executeOneNode(ctx context.Context, state S, completed map[string]bool, iterations int, usage agent.TokenUsage, nodeName string, threadID string) (StepResult[S], error) {
	// Execute the node function.
	fn := g.nodes[nodeName]
	stateCopy := g.ops.copy(state)

	result, err := fn(ctx, stateCopy)
	if err != nil {
		return StepResult[S]{}, err
	}

	// Extract usage from result.
	extractUsageFromResult[S](g.ops, &result, &usage)

	// Merge result into state.
	workingState := g.ops.copy(state)
	g.ops.merge(&workingState, result)

	// Mark completed.
	newCompleted := copyCompleted(completed)
	newCompleted[nodeName] = true

	// Serialize state for checkpoint.
	stateMap, err := g.ops.toMap(workingState)
	if err != nil {
		return StepResult[S]{}, fmt.Errorf("graph: node %q: failed to serialize state for checkpoint: %w", nodeName, err)
	}

	// Validate state serializability before saving.
	if err := validateStateSerializable(stateMap); err != nil {
		return StepResult[S]{}, err
	}

	// Build readiness set from current state keys for checkpoint and done-check.
	readinessSet := make(map[string]bool)
	for k := range stateMap {
		readinessSet[k] = true
	}

	// Save checkpoint.
	cp := Checkpoint{
		ThreadID:     threadID,
		State:        stateMap,
		Completed:    copyCompleted(newCompleted),
		ReadinessSet: readinessSet,
		Iterations:   iterations + 1,
		Usage:        usage,
		NodeName:     nodeName,
		Timestamp:    time.Now(),
	}
	saved, err := g.checkpointer.Save(ctx, threadID, cp)
	if err != nil {
		return StepResult[S]{}, err
	}

	// Determine if graph execution is done by checking if any pending nodes can become ready.
	// Check if any non-completed node has all its input keys satisfied.
	done := true
	for name, meta := range g.dataflow {
		if newCompleted[name] {
			continue
		}
		allReady := true
		for _, key := range meta.InputKeys {
			if !readinessSet[key] {
				allReady = false
				break
			}
		}
		if allReady {
			done = false
			break
		}
	}

	return StepResult[S]{
		State:    workingState,
		NodeName: nodeName,
		Version:  saved.Version,
		Done:     done,
		Usage:    usage,
	}, nil
}

// extractUsageFromResult extracts token usage from a node result.
// For struct types implementing usageCarrier, it uses the interface.
// For map types, it checks the __usage__ key.
func extractUsageFromResult[S any](ops stateOps[S], result *S, usage *agent.TokenUsage) {
	// Try usageCarrier interface first (for struct types embedding GraphState).
	if carrier, ok := any(result).(usageCarrier); ok {
		if u := carrier.getPendingUsage(); u.InputTokens > 0 || u.OutputTokens > 0 {
			usage.InputTokens += u.InputTokens
			usage.OutputTokens += u.OutputTokens
			carrier.clearPendingUsage()
		}
		return
	}

	// For map types, check the __usage__ key directly.
	if m, ok := any(result).(*map[string]any); ok {
		if u, exists := (*m)["__usage__"].(agent.TokenUsage); exists {
			usage.InputTokens += u.InputTokens
			usage.OutputTokens += u.OutputTokens
			delete(*m, "__usage__")
		}
	}
}
