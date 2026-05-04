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

	// Determine next node from routing rules applied to checkpoint's NodeName.
	nextNode, err := g.determineNextNode(ctx, cp)
	if err != nil {
		return StepResult[S]{}, err
	}
	if nextNode == "" {
		// No next node — graph is done.
		state, err := g.ops.fromMap(cp.State)
		if err != nil {
			return StepResult[S]{}, fmt.Errorf("graph: Step: failed to deserialize checkpoint state: %w", err)
		}
		return StepResult[S]{State: state, NodeName: cp.NodeName, Version: cp.Version, Done: true, Usage: cp.Usage}, nil
	}

	// Deserialize checkpoint state.
	state, err := g.ops.fromMap(cp.State)
	if err != nil {
		return StepResult[S]{}, fmt.Errorf("graph: Step: failed to deserialize checkpoint state: %w", err)
	}

	return g.executeOneNode(ctx, state, cp.Completed, cp.Iterations, cp.Usage, nextNode, threadID)
}

// Resume continues execution from the latest checkpoint for threadID.
// Optional updates are merged into the checkpointed state before continuing.
// Returns Result[S] on completion, or GraphInterruptError if another interrupt is hit.
func (g *Graph[S]) Resume(ctx context.Context, threadID string, updates *S) (Result[S], error) {
	if g.checkpointer == nil {
		return Result[S]{}, ErrNoCheckpointer
	}
	if threadID == "" {
		return Result[S]{}, ErrThreadIDRequired
	}

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

	// Emit Resumed event.
	if g.eventHook != nil {
		snapshot, _ := g.ops.toMap(resumeState)
		g.eventHook.OnEvent(GraphEvent{
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
		// InterruptAfter or normal checkpoint: determine next from routing.
		r, hasRoute := g.routes[cp.NodeName]
		if !hasRoute {
			// No outgoing route — graph is done.
			return Result[S]{State: resumeState, Usage: cp.Usage}, nil
		}
		switch {
		case r.static != "":
			workQueue = []string{r.static}
		case r.conditional != nil:
			routerState := g.ops.copy(resumeState)
			next, err2 := r.conditional(ctx, routerState)
			if err2 != nil {
				return Result[S]{}, err2
			}
			if next == "" {
				return Result[S]{State: resumeState, Usage: cp.Usage}, nil
			}
			workQueue = []string{next}
		case len(r.fork) > 0:
			// Fork: use the generic engine to handle parallel execution.
			exec := &runExec[S]{
				graph:      g,
				state:      resumeState,
				ops:        g.ops,
				nodes:      g.nodes,
				routes:     g.routes,
				completed:  copyCompleted(cp.Completed),
				iterations: cp.Iterations,
				usage:      cp.Usage,
				workQueue:  nil,
				threadID:   threadID,
			}
			if err := exec.dispatchNext(ctx, cp.NodeName); err != nil {
				if finishTrace != nil {
					finishTrace(err, exec.iterations)
				}
				return Result[S]{}, err
			}
			err = exec.execute(ctx)
			if g.eventHook != nil {
				snapshot, _ := g.ops.toMap(exec.state)
				g.eventHook.OnEvent(GraphEvent{
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
	}
	if len(workQueue) == 0 {
		return Result[S]{State: resumeState, Usage: cp.Usage}, nil
	}

	// Continue execution from next node using the generic engine.
	exec := &runExec[S]{
		graph:              g,
		state:              resumeState,
		ops:                g.ops,
		nodes:              g.nodes,
		routes:             g.routes,
		completed:          copyCompleted(cp.Completed),
		iterations:         cp.Iterations,
		usage:              cp.Usage,
		workQueue:          workQueue,
		threadID:           threadID,
		skipFirstInterrupt: skipFirst,
	}

	err = exec.execute(ctx)

	// Emit GraphCompleted event.
	if g.eventHook != nil {
		snapshot, _ := g.ops.toMap(exec.state)
		g.eventHook.OnEvent(GraphEvent{
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
		ThreadID:   threadID,
		State:      CopyState(cp.State),
		Completed:  copyCompleted(cp.Completed),
		Iterations: cp.Iterations,
		Usage:      cp.Usage,
		NodeName:   cp.NodeName,
		Timestamp:  time.Now(),
	}
	_, err = g.checkpointer.Save(ctx, threadID, rewindCp)
	if err != nil {
		return err
	}

	// Emit RewindCompleted event.
	if g.eventHook != nil {
		g.eventHook.OnEvent(GraphEvent{
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

	// Save checkpoint.
	cp := Checkpoint{
		ThreadID:   threadID,
		State:      stateMap,
		Completed:  copyCompleted(newCompleted),
		Iterations: iterations + 1,
		Usage:      usage,
		NodeName:   nodeName,
		Timestamp:  time.Now(),
	}
	saved, err := g.checkpointer.Save(ctx, threadID, cp)
	if err != nil {
		return StepResult[S]{}, err
	}

	// Determine if done (no outgoing route or router returns empty).
	nextNode, _ := g.determineNextNode(ctx, saved)
	done := nextNode == ""

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

// determineNextNode determines the next node to execute based on routing rules
// applied to the checkpoint's NodeName.
func (g *Graph[S]) determineNextNode(ctx context.Context, cp Checkpoint) (string, error) {
	r, hasRoute := g.routes[cp.NodeName]
	if !hasRoute {
		return "", nil
	}

	switch {
	case r.static != "":
		return r.static, nil
	case r.conditional != nil:
		// Deserialize checkpoint state for the router.
		state, err := g.ops.fromMap(cp.State)
		if err != nil {
			return "", fmt.Errorf("graph: determineNextNode: failed to deserialize state: %w", err)
		}
		next, err := r.conditional(ctx, state)
		if err != nil {
			return "", err
		}
		return next, nil
	case len(r.fork) > 0:
		return "", fmt.Errorf("graph: Step does not support fork/join nodes; use Run or Resume instead")
	}

	return "", nil
}
