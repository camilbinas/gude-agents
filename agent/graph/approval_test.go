package graph

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// inlineCheckpointer is a minimal in-memory GraphCheckpointer used only by
// approval tests to avoid importing graph/checkpointer/memory (which would
// create an import cycle).
type inlineCheckpointer struct {
	mu      sync.Mutex
	threads map[string][]Checkpoint
}

func newInlineCheckpointer() *inlineCheckpointer {
	return &inlineCheckpointer{threads: make(map[string][]Checkpoint)}
}

func (c *inlineCheckpointer) Save(_ context.Context, threadID string, cp Checkpoint) (Checkpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing := c.threads[threadID]
	cp.ThreadID = threadID
	cp.Version = len(existing) + 1
	if cp.Timestamp.IsZero() {
		cp.Timestamp = time.Now()
	}
	c.threads[threadID] = append(existing, cp)
	return cp, nil
}

func (c *inlineCheckpointer) Load(_ context.Context, threadID string) (Checkpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cps := c.threads[threadID]
	if len(cps) == 0 {
		return Checkpoint{}, ErrCheckpointNotFound
	}
	return cps[len(cps)-1], nil
}

func (c *inlineCheckpointer) LoadAt(_ context.Context, threadID string, version int) (Checkpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cp := range c.threads[threadID] {
		if cp.Version == version {
			return cp, nil
		}
	}
	return Checkpoint{}, ErrCheckpointNotFound
}

func (c *inlineCheckpointer) History(_ context.Context, threadID string) ([]CheckpointMeta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var metas []CheckpointMeta
	for _, cp := range c.threads[threadID] {
		metas = append(metas, CheckpointMeta{Version: cp.Version, NodeName: cp.NodeName, Timestamp: cp.Timestamp})
	}
	return metas, nil
}

func (c *inlineCheckpointer) List(_ context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for id := range c.threads {
		ids = append(ids, id)
	}
	return ids, nil
}

func (c *inlineCheckpointer) Delete(_ context.Context, threadID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.threads, threadID)
	return nil
}

// inlineScriptedProvider is a minimal scripted provider for approval tests.
type inlineScriptedProvider struct {
	mu        sync.Mutex
	responses []*agent.ProviderResponse
	idx       int
}

func newInlineScriptedProvider(responses ...*agent.ProviderResponse) *inlineScriptedProvider {
	return &inlineScriptedProvider{responses: responses}
}

func (p *inlineScriptedProvider) Name() string { return "mock" }

func (p *inlineScriptedProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	return p.ConverseStream(ctx, params, nil)
}

func (p *inlineScriptedProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idx >= len(p.responses) {
		return nil, errors.New("inlineScriptedProvider: no more responses")
	}
	resp := p.responses[p.idx]
	p.idx++
	if cb != nil && resp.Text != "" {
		cb(resp.Text)
	}
	return resp, nil
}

// TestGraph_ToolApprovalRequired_WithCheckpointer verifies the full graph
// approval flow:
//  1. Graph.Run hits a tool approval pause → returns *GraphToolApprovalError
//  2. Caller inspects the pending tool name/input
//  3. Graph.ResumeWithApproval(Allow) → graph finishes and output is written
func TestGraph_ToolApprovalRequired_WithCheckpointer(t *testing.T) {
	cp := newInlineCheckpointer()

	g, err := New[State](
		WithCheckpointer(cp),
		WithMaxIterations(10),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Track how many times the real tool handler is invoked.
	approvalCalls := 0
	deleteOrderTool := tool.NewRaw(
		"delete_order",
		"Delete an order",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
			"required":   []string{"order_id"},
		},
		func(_ context.Context, input json.RawMessage) (string, error) {
			approvalCalls++
			var p struct {
				OrderID string `json:"order_id"`
			}
			json.Unmarshal(input, &p)
			return `{"deleted":true,"order_id":"` + p.OrderID + `"}`, nil
		},
		tool.RequiresApproval(),
	)

	// Scripted provider:
	// - First call: LLM requests delete_order (triggers approval pause).
	// - Second call: after approval + tool execution, LLM returns final text.
	prov := newInlineScriptedProvider(
		&agent.ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "tu-g1",
				Name:      "delete_order",
				Input:     json.RawMessage(`{"order_id":"42"}`),
			}},
		},
		&agent.ProviderResponse{Text: "Order 42 has been deleted."},
	)

	a, err := agent.New(prov, prompt.Text("helpful"), []tool.Tool{deleteOrderTool})
	if err != nil {
		t.Fatal(err)
	}

	agentNode, err := g.Agent("process", a, Out("output"), In("input"))
	if err != nil {
		t.Fatal(err)
	}
	g.Start(agentNode.Name())

	threadID := "graph-approval-test-1"
	initialState := State{"input": "delete order 42"}

	// --- Run 1: should pause for approval ---
	_, err = g.Run(context.Background(), initialState, WithThreadID(threadID))

	var ae *GraphToolApprovalError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *GraphToolApprovalError, got %T: %v", err, err)
	}
	if ae.Approval.ToolName != "delete_order" {
		t.Errorf("ToolName = %q, want %q", ae.Approval.ToolName, "delete_order")
	}
	if ae.Interrupt.Checkpoint.Version == 0 {
		t.Error("expected checkpoint version > 0 after approval pause")
	}

	// --- Resume with approval ---
	result, err := g.ResumeWithApproval(context.Background(), ae, tool.Allow())
	if err != nil {
		t.Fatalf("ResumeWithApproval failed: %v", err)
	}

	output, _ := result.State["output"].(string)
	if output != "Order 42 has been deleted." {
		t.Errorf("output = %q, want %q", output, "Order 42 has been deleted.")
	}
	if approvalCalls != 1 {
		t.Errorf("tool handler called %d times, want 1", approvalCalls)
	}
}

// TestGraph_ToolApprovalRequired_Deny verifies that denying injects a denial
// result and the agent continues without running the handler.
func TestGraph_ToolApprovalRequired_Deny(t *testing.T) {
	cp := newInlineCheckpointer()

	g, err := New[State](WithCheckpointer(cp), WithMaxIterations(10))
	if err != nil {
		t.Fatal(err)
	}

	handlerCalled := false
	deleteOrderTool := tool.NewRaw(
		"delete_order",
		"Delete an order",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			handlerCalled = true
			return `{"deleted":true}`, nil
		},
		tool.RequiresApproval(),
	)

	prov := newInlineScriptedProvider(
		&agent.ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "tu-g2",
				Name:      "delete_order",
				Input:     json.RawMessage(`{"order_id":"99"}`),
			}},
		},
		&agent.ProviderResponse{Text: "I couldn't delete order 99 — access was denied."},
	)

	a, err := agent.New(prov, prompt.Text("helpful"), []tool.Tool{deleteOrderTool})
	if err != nil {
		t.Fatal(err)
	}

	agentNode, err := g.Agent("process", a, Out("output"), In("input"))
	if err != nil {
		t.Fatal(err)
	}
	g.Start(agentNode.Name())

	threadID := "graph-approval-deny-1"
	_, err = g.Run(context.Background(), State{"input": "delete order 99"}, WithThreadID(threadID))

	var ae *GraphToolApprovalError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *GraphToolApprovalError, got %T: %v", err, err)
	}

	result, err := g.ResumeWithApproval(context.Background(), ae, tool.Deny("access denied by admin"))
	if err != nil {
		t.Fatalf("ResumeWithApproval(deny) failed: %v", err)
	}

	if handlerCalled {
		t.Error("tool handler should not have been called on denial")
	}

	output, _ := result.State["output"].(string)
	if output == "" {
		t.Error("expected non-empty output after denied approval")
	}
}

// TestGraph_ToolApprovalRequired_NoCheckpointer verifies that without a
// checkpointer the error still surfaces with the correct type.
func TestGraph_ToolApprovalRequired_NoCheckpointer(t *testing.T) {
	g, err := New[State]()
	if err != nil {
		t.Fatal(err)
	}

	deleteOrderTool := tool.NewRaw(
		"delete_order",
		"Delete an order",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return `{"deleted":true}`, nil
		},
		tool.RequiresApproval(),
	)

	prov := newInlineScriptedProvider(
		&agent.ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "tu-g3",
				Name:      "delete_order",
				Input:     json.RawMessage(`{"order_id":"77"}`),
			}},
		},
	)

	a, err := agent.New(prov, prompt.Text("helpful"), []tool.Tool{deleteOrderTool})
	if err != nil {
		t.Fatal(err)
	}

	agentNode, err := g.Agent("process", a, Out("output"), In("input"))
	if err != nil {
		t.Fatal(err)
	}
	g.Start(agentNode.Name())

	_, err = g.Run(context.Background(), State{"input": "delete order 77"})

	var ae *GraphToolApprovalError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *GraphToolApprovalError without checkpointer, got %T: %v", err, err)
	}
	if ae.Approval.ToolName != "delete_order" {
		t.Errorf("ToolName = %q, want %q", ae.Approval.ToolName, "delete_order")
	}
}
