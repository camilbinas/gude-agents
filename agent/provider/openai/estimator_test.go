package openai

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

type stubTokenEstimator struct {
	count int
	err   error
	got   agent.ModelRequest
}

func (s *stubTokenEstimator) EstimateTokens(_ context.Context, req agent.ModelRequest) (int, error) {
	s.got = req
	return s.count, s.err
}

func TestEstimator_DelegatesModelRequest(t *testing.T) {
	delegate := &stubTokenEstimator{count: 42}
	est := NewEstimator(delegate)
	req := agent.ModelRequest{
		Messages: []agent.Message{{
			Role:    agent.RoleUser,
			Content: []agent.ContentBlock{agent.TextBlock{Text: "Use a tool"}},
		}},
		System: "You are helpful.",
		Tools: []tool.Spec{{
			Name:        "search",
			Description: "Search the web",
			InputSchema: map[string]any{"type": "object"},
		}},
		CachingEnabled: true,
	}

	got, err := est.EstimateTokens(context.Background(), req)
	if err != nil {
		t.Fatalf("EstimateTokens() error = %v", err)
	}
	if got != 42 {
		t.Fatalf("EstimateTokens() = %d, want 42", got)
	}
	if !reflect.DeepEqual(delegate.got, req) {
		t.Fatalf("delegated request = %#v, want %#v", delegate.got, req)
	}
}

func TestEstimator_PropagatesError(t *testing.T) {
	wantErr := errors.New("count failed")
	est := NewEstimator(&stubTokenEstimator{err: wantErr})

	got, err := est.EstimateTokens(context.Background(), agent.ModelRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("EstimateTokens() error = %v, want %v", err, wantErr)
	}
	if got != 0 {
		t.Fatalf("EstimateTokens() = %d, want 0", got)
	}
}
