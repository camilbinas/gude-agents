package agent

import (
	"errors"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// toolBatchCoordinator is the shared durable completion boundary for a model
// tool batch. It validates and prepares active calls, dispatches them, projects
// only definitive results into canonical history, checkpoints those results,
// and reports a pause without ever projecting indeterminate or coordination
// outcomes.
type toolBatchCoordinator struct {
	r         *run
	context   *Context
	calls     []tool.Call
	available map[string]tool.Tool
	decisions []*tool.Decision
	messages  *turnMessages
}

type coordinatedToolBatch struct {
	outcomes  []toolOutcome
	interrupt *Interrupt
	err       error
}

func (r *run) coordinateToolBatch(c *Context, calls []tool.Call, available map[string]tool.Tool, decisions []*tool.Decision, messages *turnMessages) coordinatedToolBatch {
	return toolBatchCoordinator{
		r: r, context: c, calls: calls, available: available, decisions: decisions, messages: messages,
	}.run()
}

func (b toolBatchCoordinator) run() coordinatedToolBatch {
	calls, decisions, err := b.r.prepareToolBatch(b.messages.canonical, b.calls, b.decisions)
	if err != nil {
		return coordinatedToolBatch{err: err}
	}
	if len(calls) == 0 {
		return coordinatedToolBatch{}
	}
	outcomes := b.r.executeBatch(b.context, calls, b.available, decisions)
	result := coordinatedToolBatch{outcomes: outcomes}

	// A coordination loss means another worker owns the active boundary. Do
	// not append or checkpoint even definitive siblings: only that worker (or
	// recovery) may establish the canonical completion set.
	for _, outcome := range outcomes {
		if outcome.kind == toolOutcomeCoordinationFailure {
			result.err = outcome.err
			if result.err == nil {
				result.err = ErrExecutionRecoveryUnsupported
			}
			return result
		}
	}
	appendDefinitive := func() []ContentBlock {
		definitive := resultBlocks(outcomes)
		if len(definitive) > 0 {
			// Result messages are append-only. Resume and recovery never rewrite
			// a result message the conversation store already owns.
			b.messages.appendCanonical(Message{Role: RoleUser, Content: definitive})
		}
		return definitive
	}
	if err := batchOutcomeError(outcomes); err != nil {
		if definitive := appendDefinitive(); len(definitive) > 0 {
			if checkpointErr := b.r.checkpointToolResults(b.messages.canonical, outcomes); checkpointErr != nil {
				result.err = errors.Join(err, checkpointErr)
				return result
			}
		}
		result.err = err
		return result
	}
	if in := b.r.pendingInterrupt(outcomes, calls); in != nil {
		// Persist the pause payload before definitive tool results can clear the
		// active ToolBatch. If the final paused transition loses its acknowledgement,
		// RecoverExecution can project this exact pause without replaying a handler.
		if err := b.r.persistPendingPause(in); err != nil {
			result.err = err
			return result
		}
		appendDefinitive()
		if err := b.r.checkpointToolResults(b.messages.canonical, outcomes); err != nil {
			result.err = err
			return result
		}
		result.interrupt = in
		return result
	}
	appendDefinitive()
	if err := b.r.checkpointToolResults(b.messages.canonical, outcomes); err != nil {
		result.err = err
	}
	return result
}
