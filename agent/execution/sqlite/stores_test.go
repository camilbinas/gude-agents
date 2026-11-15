package sqlite_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"

	sqlitestores "github.com/camilbinas/gude-agents/agent/execution/sqlite"
)

// scriptedProvider pops one pre-set response per Stream call.
type scriptedProvider struct {
	mu        sync.Mutex
	responses []*agent.ModelResponse
	i         int
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Stream(_ context.Context, _ agent.ModelRequest, _ func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.i >= len(p.responses) {
		return &agent.ModelResponse{Text: "done"}, nil
	}
	r := p.responses[p.i]
	p.i++
	return r, nil
}

func deleteOrderTool() tool.Tool {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
		"required":   []string{"order_id"},
	}
	return tool.NewRaw("delete_order", "Permanently deletes an order", schema,
		func(_ context.Context, input json.RawMessage) (string, error) {
			var p struct {
				OrderID string `json:"order_id"`
			}
			_ = json.Unmarshal(input, &p)
			return `{"deleted":true,"order_id":"` + p.OrderID + `"}`, nil
		},
		tool.RequiresApproval(),
	)
}

// A paused approval survives closing both stores and reopening the one file
// with Open: a fresh agent built from the reopened stores resumes and finishes.
func TestOpenPauseReopenResume(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "agent.db")
	ctx := agent.Background().WithConversationID("open-resume")

	// First process: pause on the approval-required tool.
	stores, err := sqlitestores.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pausing := &scriptedProvider{responses: []*agent.ModelResponse{{
		ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"ORD-9"}`)}},
	}}}
	a1, err := agent.Agent(pausing, "helpful", append(stores.Options(), agent.WithTools(deleteOrderTool()))...)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a1.Invoke(ctx, "delete order 9")
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.StopReason != agent.StopInterrupt || res.Interrupt == nil {
		t.Fatalf("StopReason = %q, Interrupt = %v; want a pause", res.StopReason, res.Interrupt)
	}
	execID := res.Interrupt.ExecutionID
	if execID == "" {
		t.Fatal("interrupt must carry an execution id")
	}
	if err := stores.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second process: reopen the same file, build a fresh agent, resume.
	reopened, err := sqlitestores.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	resuming := &scriptedProvider{responses: []*agent.ModelResponse{{Text: "order 9 deleted"}}}
	a2, err := agent.Agent(resuming, "helpful", append(reopened.Options(), agent.WithTools(deleteOrderTool()))...)
	if err != nil {
		t.Fatal(err)
	}
	in, err := a2.LoadInterrupt(context.Background(), execID)
	if err != nil {
		t.Fatalf("LoadInterrupt: %v", err)
	}
	final, err := a2.Resume(agent.NewContext(context.Background()), in, agent.Approve())
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.StopReason != agent.StopEndTurn || final.Text != "order 9 deleted" {
		t.Fatalf("resumed result = (%q, %q); want (end_turn, \"order 9 deleted\")", final.StopReason, final.Text)
	}
}

// Open wires both halves on one file and Options registers each exactly once.
func TestOpenOptionsRegisterBothStores(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "agent.db")
	stores, err := sqlitestores.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer stores.Close()
	if stores.Conversations == nil || stores.Executions == nil {
		t.Fatal("Open must set both Conversations and Executions")
	}
	if n := len(stores.Options()); n != 2 {
		t.Fatalf("Options() = %d options, want 2", n)
	}
	// Both options apply cleanly to a real agent.
	if _, err := agent.Agent(&scriptedProvider{}, "sys", stores.Options()...); err != nil {
		t.Fatalf("Agent with Open stores: %v", err)
	}
}

func TestOpenEmptyDSN(t *testing.T) {
	if _, err := sqlitestores.Open(""); err == nil {
		t.Fatal("empty dsn must fail")
	}
}
