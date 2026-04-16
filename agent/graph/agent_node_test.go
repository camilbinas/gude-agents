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

	t.Run("8.3 accumulates TokenUsage into GraphResult", func(t *testing.T) {
		sp := newScriptedProvider(&agent.ProviderResponse{
			Text:  "response",
			Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
		})
		a, err := agent.New(sp, prompt.Text("you are a test agent"), nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		g, err := graph.NewGraph()
		if err != nil {
			t.Fatalf("NewGraph: %v", err)
		}
		if err := g.AddNode("agent", graph.AgentNode(a, "input", "output")); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
		g.SetEntry("agent")

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
