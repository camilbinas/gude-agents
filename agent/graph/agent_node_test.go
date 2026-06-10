package graph_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// scriptedProvider returns a pre-configured sequence of ProviderResponses.
type scriptedProvider struct {
	mu        sync.Mutex
	responses []*agent.ProviderResponse
	callIndex int
}

func newScriptedProvider(responses ...*agent.ProviderResponse) *scriptedProvider {
	return &scriptedProvider{responses: responses}
}

func (sp *scriptedProvider) Name() string { return "mock" }

func (sp *scriptedProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.callIndex >= len(sp.responses) {
		return nil, fmt.Errorf("scriptedProvider: no more responses (call %d)", sp.callIndex)
	}
	resp := sp.responses[sp.callIndex]
	sp.callIndex++
	return resp, nil
}

func (sp *scriptedProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.callIndex >= len(sp.responses) {
		return nil, fmt.Errorf("scriptedProvider: no more responses (call %d)", sp.callIndex)
	}
	resp := sp.responses[sp.callIndex]
	sp.callIndex++

	if len(resp.ToolCalls) == 0 && resp.Text != "" && cb != nil {
		words := strings.Fields(resp.Text)
		for i, w := range words {
			if i > 0 {
				cb(" ")
			}
			cb(w)
		}
	}
	return resp, nil
}

// errorProvider always returns an error from ConverseStream.
type errorProvider struct{ err error }

func (ep errorProvider) Name() string { return "mock" }

func (ep errorProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	return nil, ep.err
}
func (ep errorProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, _ agent.StreamCallback) (*agent.ProviderResponse, error) {
	return nil, ep.err
}

func TestAgentNode(t *testing.T) {
	t.Run("8.1 reads from inputKey and writes response to outputKey", func(t *testing.T) {
		sp := newScriptedProvider(&agent.ProviderResponse{Text: "echo: hello"})
		a, err := agent.New(sp, prompt.Text("you are a test agent"), nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		fn := graph.AgentNode(a, "input", "output")
		state := graph.State{"input": "hello"}
		result, err := fn(context.Background(), state)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["output"] != "echo: hello" {
			t.Errorf("expected output=%q, got %v", "echo: hello", result["output"])
		}
		// original input key should still be present
		if result["input"] != "hello" {
			t.Errorf("expected input key preserved, got %v", result["input"])
		}
	})

	t.Run("8.2 propagates agent error as node error", func(t *testing.T) {
		providerErr := errors.New("provider failure")
		ep := errorProvider{err: providerErr}
		a, err := agent.New(ep, prompt.Text("you are a test agent"), nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		fn := graph.AgentNode(a, "input", "output")
		_, nodeErr := fn(context.Background(), graph.State{"input": "hello"})
		if nodeErr == nil {
			t.Fatal("expected error, got nil")
		}
		// The error should wrap the provider error
		if !errors.Is(nodeErr, providerErr) {
			// Check via unwrap chain — ProviderError wraps the cause
			var pe *agent.ProviderError
			if !errors.As(nodeErr, &pe) {
				t.Fatalf("expected error chain to contain providerErr or *ProviderError, got %T: %v", nodeErr, nodeErr)
			}
		}
	})

	t.Run("8.3 accumulates TokenUsage into Result", func(t *testing.T) {
		sp := newScriptedProvider(&agent.ProviderResponse{
			Text:  "response",
			Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
		})
		a, err := agent.New(sp, prompt.Text("you are a test agent"), nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		g, err := graph.New[graph.State]()
		if err != nil {
			t.Fatalf("New[State]: %v", err)
		}
		if _, err := g.Node("agent", graph.AgentNode(a, "input", "output"), graph.In(), graph.Out("output")); err != nil {
			t.Fatalf("Node: %v", err)
		}
		g.Start("agent")

		res, err := g.Run(context.Background(), graph.State{"input": "hello"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Usage.InputTokens != 10 {
			t.Errorf("expected InputTokens=10, got %d", res.Usage.InputTokens)
		}
		if res.Usage.OutputTokens != 5 {
			t.Errorf("expected OutputTokens=5, got %d", res.Usage.OutputTokens)
		}
		if res.State["output"] != "response" {
			t.Errorf("expected output=%q, got %v", "response", res.State["output"])
		}
	})
}

// TestAgentWithAccessor_AccumulatesUsage verifies that a typed agent node added
// via AgentWithAccessor propagates the agent's token usage into Result.Usage.
//
// This locks in the fix for the previously-silent gap where the typed agent
// path (buildAgentNodeFunc) did not report usage at all — only the map-based
// AgentNode did. Usage is now reported via graph.AddUsage(ctx, c.Usage()).
func TestAgentWithAccessor_AccumulatesUsage(t *testing.T) {
	type MyState struct {
		Input  string `json:"input"`
		Output string `json:"output"`
	}

	sp := newScriptedProvider(&agent.ProviderResponse{
		Text:  "typed response",
		Usage: agent.TokenUsage{InputTokens: 12, OutputTokens: 7},
	})
	a, err := agent.New(sp, prompt.Text("you are a typed test agent"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	g, err := graph.New[MyState]()
	if err != nil {
		t.Fatalf("New[MyState]: %v", err)
	}

	accessor := graph.AgentNodeAccessor[MyState]{
		GetInput:   func(s MyState) string { return s.Input },
		SetOutput:  func(s *MyState, out string) { s.Output = out },
		InputKeys:  []string{"input"},
		OutputKeys: []string{"output"},
	}
	if _, err := g.AgentWithAccessor("agent", a, accessor); err != nil {
		t.Fatalf("AgentWithAccessor: %v", err)
	}
	g.Start("agent")

	res, err := g.Run(context.Background(), MyState{Input: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Usage.InputTokens != 12 {
		t.Errorf("expected InputTokens=12, got %d", res.Usage.InputTokens)
	}
	if res.Usage.OutputTokens != 7 {
		t.Errorf("expected OutputTokens=7, got %d", res.Usage.OutputTokens)
	}
	if res.State.Output != "typed response" {
		t.Errorf("expected Output=%q, got %q", "typed response", res.State.Output)
	}
}
