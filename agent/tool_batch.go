package agent

import (
	"errors"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// toolBatchCoordinator is the shared durable completion boundary for a model
// tool batch. It executes calls, projects only definitive results into the
// canonical transcript, checkpoints those results, and reports an interrupt
// without ever projecting an indeterminate or coordination outcome.
type toolBatchCoordinator struct {
	r         *run
	context   *Context
	calls     []tool.Call
	available map[string]tool.Tool
	decisions []*tool.Decision
	messages  *[]Message
	canonical func([]Message) []Message
}

type coordinatedToolBatch struct {
	outcomes  []toolOutcome
	interrupt *Interrupt
	err       error
}

func (r *run) coordinateToolBatch(c *Context, calls []tool.Call, available map[string]tool.Tool, decisions []*tool.Decision, messages *[]Message, canonical func([]Message) []Message) coordinatedToolBatch {
	return toolBatchCoordinator{
		r: r, context: c, calls: calls, available: available, decisions: decisions,
		messages: messages, canonical: canonical,
	}.run()
}

func (b toolBatchCoordinator) run() coordinatedToolBatch {
	outcomes := b.r.executeBatch(b.context, b.calls, b.available, b.decisions)
	result := coordinatedToolBatch{outcomes: outcomes}
	appendDefinitive := func() []ContentBlock {
		definitive := resultBlocks(outcomes)
		if len(definitive) > 0 {
			// Result messages are append-only. In particular, resume/recovery
			// never rewrites a result message that the conversation store already
			// owns.
			*b.messages = append(*b.messages, Message{Role: RoleUser, Content: definitive})
		}
		return definitive
	}
	if err := batchOutcomeError(outcomes); err != nil {
		if definitive := appendDefinitive(); len(definitive) > 0 {
			if checkpointErr := b.r.checkpointToolResults(b.canonical(*b.messages), outcomes); checkpointErr != nil {
				result.err = errors.Join(err, checkpointErr)
				return result
			}
		}
		result.err = err
		return result
	}
	if in := b.r.pendingInterrupt(outcomes, b.calls); in != nil {
		appendDefinitive()
		if err := b.r.checkpointToolResults(b.canonical(*b.messages), outcomes); err != nil {
			result.err = err
			return result
		}
		result.interrupt = in
		return result
	}
	appendDefinitive()
	if err := b.r.checkpointToolResults(b.canonical(*b.messages), outcomes); err != nil {
		result.err = err
	}
	return result
}
