package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/execution"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// scriptedProvider returns a fixed sequence of model responses.
type scriptedProvider struct {
	mu        sync.Mutex
	responses []*agent.ModelResponse
	next      int
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Stream(_ context.Context, _ agent.ModelRequest, _ func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next >= len(p.responses) {
		return nil, errors.New("scripted provider: no more responses")
	}
	response := p.responses[p.next]
	p.next++
	return response, nil
}

func approvalProvider() *scriptedProvider {
	return &scriptedProvider{responses: []*agent.ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "approve", Input: json.RawMessage(`{}`)}}},
		{Text: "done"},
	}}
}

func approvalTool(ran *int) tool.Tool {
	return tool.NewRaw("approve", "needs approval", nil, func(context.Context, json.RawMessage) (string, error) {
		*ran++
		return "ok", nil
	}, tool.RequiresApproval())
}

// NewMemory satisfies the root contract and each instance is its own
// process-local world.
func TestNewMemoryIsProcessLocalExecutionStore(t *testing.T) {
	var first agent.ExecutionStore = execution.NewMemory()
	var second agent.ExecutionStore = execution.NewMemory()
	ctx := context.Background()

	created, err := first.Create(ctx, agent.Execution{ID: "e-1", Status: agent.ExecutionRunning})
	if err != nil || created.Version == 0 {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	if loaded, err := first.Load(ctx, "e-1"); err != nil || loaded.ID != "e-1" {
		t.Fatalf("Load from the same store = %+v, %v", loaded, err)
	}
	if _, err := second.Load(ctx, "e-1"); !errors.Is(err, agent.ErrExecutionNotFound) {
		t.Fatalf("another memory store observed the execution: %v", err)
	}
	if _, err := first.Save(ctx, created, created.Version+5); !errors.Is(err, agent.ErrExecutionConflict) {
		t.Fatalf("stale Save = %v, want ErrExecutionConflict", err)
	}
}

// The concise explicit setup: both memory-backed choices are visible where the
// agent is composed.
func TestPauseAndResumeWithExplicitMemoryStores(t *testing.T) {
	var ran int
	a, err := agent.New(approvalProvider(), "sys",
		agent.WithTools(approvalTool(&ran)),
		agent.WithConversationStore(conversation.NewInMemory()),
		agent.WithExecutionStore(execution.NewMemory()),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := agent.Background().WithConversationID("memory-pause")
	paused, err := a.Invoke(ctx, "go")
	if err != nil || paused.Interrupt == nil || paused.Interrupt.Type != agent.InterruptApproval {
		t.Fatalf("pause = %+v, %v", paused, err)
	}
	if ran != 0 {
		t.Fatal("gated tool ran before approval")
	}
	result, err := a.Resume(ctx, paused.Interrupt, agent.Approve())
	if err != nil || result.Text != "done" || ran != 1 {
		t.Fatalf("resume = %+v, %v, handler runs = %d", result, err, ran)
	}
}

// execution.New adapts any checkpoint.Checkpointer. Because the paused state
// lives in the shared store, a different Agent instance (a restarted process in
// production) can load and resume the pause.
func TestPauseSurvivesAgentInstanceWithCheckpointBackedStore(t *testing.T) {
	conversations := conversation.NewInMemory()
	executions := execution.New(checkpoint.NewMemory())

	var firstRan int
	first, err := agent.New(approvalProvider(), "sys",
		agent.WithTools(approvalTool(&firstRan)),
		agent.WithConversationStore(conversations),
		agent.WithExecutionStore(executions),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := agent.Background().WithConversationID("durable-pause")
	paused, err := first.Invoke(ctx, "go")
	if err != nil || paused.Interrupt == nil {
		t.Fatalf("pause = %+v, %v", paused, err)
	}

	var secondRan int
	second, err := agent.New(&scriptedProvider{responses: []*agent.ModelResponse{{Text: "resumed elsewhere"}}}, "sys",
		agent.WithTools(approvalTool(&secondRan)),
		agent.WithConversationStore(conversations),
		agent.WithExecutionStore(executions),
	)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := second.LoadInterrupt(context.Background(), paused.ExecutionID)
	if err != nil || loaded.ExecutionID != paused.ExecutionID {
		t.Fatalf("LoadInterrupt = %+v, %v", loaded, err)
	}
	result, err := second.Resume(agent.Background(), loaded, agent.Approve())
	if err != nil || result.Text != "resumed elsewhere" {
		t.Fatalf("resume on second agent = %+v, %v", result, err)
	}
	if firstRan != 0 || secondRan != 1 {
		t.Fatalf("handler runs: first=%d second=%d, want 0 and 1", firstRan, secondRan)
	}
	if _, err := first.Resume(ctx, paused.Interrupt, agent.Approve()); !errors.Is(err, agent.ErrExecutionConflict) {
		t.Fatalf("second claim of the same pause = %v, want ErrExecutionConflict", err)
	}
}
