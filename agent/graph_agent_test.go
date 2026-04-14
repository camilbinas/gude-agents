package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

func TestAgentNode(t *testing.T) {
	t.Run("8.1 reads from inputKey and writes response to outputKey", func(t *testing.T) {
		sp := newScriptedProvider(&ProviderResponse{Text: "echo: hello"})
		a, err := New(sp, prompt.Text("you are a test agent"), nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		fn := AgentNode(a, "input", "output")
		state := State{"input": "hello"}
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
		a, err := New(ep, prompt.Text("you are a test agent"), nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		fn := AgentNode(a, "input", "output")
		_, nodeErr := fn(context.Background(), State{"input": "hello"})
		if nodeErr == nil {
			t.Fatal("expected error, got nil")
		}
		// The error should wrap the provider error
		if !errors.Is(nodeErr, providerErr) {
			// Check via unwrap chain — ProviderError wraps the cause
			var pe *ProviderError
			if !errors.As(nodeErr, &pe) {
				t.Fatalf("expected error chain to contain providerErr or *ProviderError, got %T: %v", nodeErr, nodeErr)
			}
		}
	})

	t.Run("8.3 accumulates TokenUsage into GraphResult", func(t *testing.T) {
		sp := newScriptedProvider(&ProviderResponse{
			Text:  "response",
			Usage: TokenUsage{InputTokens: 10, OutputTokens: 5},
		})
		a, err := New(sp, prompt.Text("you are a test agent"), nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		g := mustGraph(t)
		mustAddNode(t, g, "agent", AgentNode(a, "input", "output"))
		g.SetEntry("agent")

		res, err := g.Run(context.Background(), State{"input": "hello"})
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
