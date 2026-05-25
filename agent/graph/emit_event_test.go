package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestEmitEvent_FromNode verifies a graph node can emit a custom event
// onto the active RunEventStream channel via graph.EmitEvent.
func TestEmitEvent_FromNode(t *testing.T) {
	g := mustGraph(t)

	mustAddNodeWithKeys(t, g, "emit",
		func(ctx context.Context, s State) (State, error) {
			EmitEvent(ctx, "node.progress", map[string]any{"step": 1, "of": 3})
			out := CopyState(s)
			out["done"] = true
			return out, nil
		},
		[]string{"done"}, []string{},
	)

	stream := g.RunEventStream(context.Background(), State{})
	events := drainGraphEvents(stream)

	var got *GraphEvent
	for i := range events {
		if events[i].Type == EventCustom {
			got = &events[i]
			break
		}
	}
	if got == nil {
		t.Fatal("expected EventCustom on the stream, got none")
	}
	if got.NodeName != "emit" {
		t.Errorf("NodeName=%q, want emit", got.NodeName)
	}
	if got.CustomName != "node.progress" {
		t.Errorf("CustomName=%q, want node.progress", got.CustomName)
	}
	var payload struct {
		Step int `json:"step"`
		Of   int `json:"of"`
	}
	if err := json.Unmarshal(got.CustomPayload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload.Step != 1 || payload.Of != 3 {
		t.Errorf("payload=%+v, want {1 3}", payload)
	}
}

// TestEmitEvent_NoActiveStream is a no-op when the graph runs via plain Run
// with no event hook configured. The node's call to EmitEvent must not
// panic and must not affect graph execution.
func TestEmitEvent_NoActiveStream(t *testing.T) {
	g := mustGraph(t)
	mustAddNodeWithKeys(t, g, "emit",
		func(ctx context.Context, s State) (State, error) {
			EmitEvent(ctx, "node.progress", "anything")
			out := CopyState(s)
			out["done"] = true
			return out, nil
		},
		[]string{"done"}, []string{},
	)

	if _, err := g.Run(context.Background(), State{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestEmitEvent_BridgedFromAgentNode verifies that a custom event emitted
// by a tool handler inside an agent node bubbles up to the graph's
// RunEventStream channel as EventCustom, with NodeName attributed to the
// graph node.
func TestEmitEvent_BridgedFromAgentNode(t *testing.T) {
	// Inline scripted provider: 1st call asks for the emit tool, 2nd returns final text.
	p := &emitTestProvider{
		responses: []*agent.ProviderResponse{
			{ToolCalls: []tool.Call{
				{ToolUseID: "1", Name: "emit_progress", Input: json.RawMessage(`{}`)},
			}},
			{Text: "done"},
		},
	}

	emitTool := tool.New("emit_progress", "emit a progress event",
		func(ctx context.Context, _ struct{}) (string, error) {
			c := agent.FromContext(ctx)
			if c == nil {
				t.Fatal("expected *agent.Context inside tool handler")
			}
			c.EmitEvent("rag.retrieved", map[string]any{"docs": 5})
			return "ok", nil
		},
	)

	a, err := agent.New(p, prompt.Text("test"), []tool.Tool{emitTool})
	if err != nil {
		t.Fatal(err)
	}

	g := mustGraph(t)
	if _, err := g.Agent("worker", a, Keys("answer", "input")); err != nil {
		t.Fatalf("Agent: %v", err)
	}
	g.Start("worker")

	stream := g.RunEventStream(context.Background(), State{"input": "go"})
	events := drainGraphEvents(stream)

	var got *GraphEvent
	for i := range events {
		if events[i].Type == EventCustom {
			got = &events[i]
			break
		}
	}
	if got == nil {
		t.Fatal("expected EventCustom on the stream from the bridged agent tool, got none")
	}
	if got.NodeName != "worker" {
		t.Errorf("NodeName=%q, want worker (the graph node hosting the agent)", got.NodeName)
	}
	if got.CustomName != "rag.retrieved" {
		t.Errorf("CustomName=%q, want rag.retrieved", got.CustomName)
	}
}

// emitTestProvider is a tiny scripted Provider used only by the bridged
// custom-event test. It pops one response per call and supports both the
// streaming and non-streaming Converse paths.
type emitTestProvider struct {
	mu        sync.Mutex
	responses []*agent.ProviderResponse
	idx       int
}

func (p *emitTestProvider) Name() string { return "mock" }

func (p *emitTestProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idx >= len(p.responses) {
		return nil, fmt.Errorf("emitTestProvider: no more responses (call %d)", p.idx)
	}
	r := p.responses[p.idx]
	p.idx++
	return r, nil
}

func (p *emitTestProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idx >= len(p.responses) {
		return nil, fmt.Errorf("emitTestProvider: no more responses (call %d)", p.idx)
	}
	r := p.responses[p.idx]
	p.idx++
	if cb != nil && r.Text != "" {
		cb(r.Text)
	}
	return r, nil
}
