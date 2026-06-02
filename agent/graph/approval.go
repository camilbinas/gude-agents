package graph

import (
	"context"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

type graphApprovalKey struct{}

type graphApprovalDecision struct {
	Request  *agent.ApprovalRequest
	Decision tool.Decision
}

// GraphToolApprovalError is returned by Graph.Run when an agent node calls a
// tool marked with RequiresApproval. Use errors.As to extract it, then call
// Graph.ResumeWithApproval with the human decision.
//
//	var ae *graph.GraphToolApprovalError
//	if errors.As(err, &ae) {
//	    _, err = g.ResumeWithApproval(ctx, ae, tool.Allow())
//	}
type GraphToolApprovalError struct {
	Approval  *agent.ApprovalRequest
	Interrupt InterruptResult
}

func (e *GraphToolApprovalError) Error() string {
	return fmt.Sprintf("graph: tool approval required for %q at node %q",
		e.Approval.ToolName, e.Interrupt.NodeName)
}

// ResumeWithApproval continues graph execution after a tool approval decision.
func (g *Graph[S]) ResumeWithApproval(ctx context.Context, ae *GraphToolApprovalError, decision tool.Decision, opts ...RunOption) (Result[S], error) {
	if g.checkpointer == nil {
		return Result[S]{}, ErrNoCheckpointer
	}

	threadID := ae.Interrupt.Checkpoint.ThreadID
	if threadID == "" {
		return Result[S]{}, ErrThreadIDRequired
	}

	ctx = context.WithValue(ctx, graphApprovalKey{}, &graphApprovalDecision{
		Request:  ae.Approval,
		Decision: decision,
	})

	return g.Resume(ctx, threadID, nil, opts...)
}
