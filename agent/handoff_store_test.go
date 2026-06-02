package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// --- in-process HandoffStore for testing ---

type testHandoffStore struct {
	mu      sync.Mutex
	pending map[string]*HandoffRequest
	saved   int
	deleted int
}

func newTestHandoffStore() *testHandoffStore {
	return &testHandoffStore{pending: make(map[string]*HandoffRequest)}
}

func (s *testHandoffStore) SaveHandoff(_ context.Context, convID string, hr *HandoffRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[convID] = hr
	s.saved++
	return nil
}

func (s *testHandoffStore) LoadHandoff(_ context.Context, convID string) (*HandoffRequest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hr, ok := s.pending[convID]
	return hr, ok, nil
}

func (s *testHandoffStore) DeleteHandoff(_ context.Context, convID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, convID)
	s.deleted++
	return nil
}

// TestHandoffStore_AutoSaveOnHandoff verifies that when a HandoffStore is
// configured and the agent returns ErrHandoffRequested, the HandoffRequest is
// automatically persisted to the store.
func TestHandoffStore_AutoSaveOnHandoff(t *testing.T) {
	provider := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "h1",
				Name:      "ask_human",
				Input:     json.RawMessage(`{"reason":"need approval","question":"Approve?"}`),
			}},
		},
	)

	store := newTestHandoffStore()
	mem := newTestMemoryStore()

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{NewHandoffTool("ask_human", "")},
		WithHandoffStore(store),
		WithConversation(mem, "conv-1"),
	)
	if err != nil {
		t.Fatal(err)
	}

	c := Background()
	err = a.InvokeStream(c, "I need a refund", nil)
	if !errors.Is(err, ErrHandoffRequested) {
		t.Fatalf("expected ErrHandoffRequested, got %v", err)
	}

	// Should have been auto-saved.
	if store.saved != 1 {
		t.Fatalf("expected 1 save, got %d", store.saved)
	}
	hr, ok, err := store.LoadHandoff(context.Background(), "conv-1")
	if err != nil || !ok {
		t.Fatalf("expected handoff in store: ok=%v err=%v", ok, err)
	}
	if hr.Reason != "need approval" {
		t.Errorf("reason = %q, want %q", hr.Reason, "need approval")
	}
	if len(hr.Messages) == 0 {
		t.Error("expected non-empty messages snapshot in stored HandoffRequest")
	}
}

// TestHandoffStore_AutoDeleteOnResume verifies that after a successful Resume,
// the persisted HandoffRequest is removed from the store.
func TestHandoffStore_AutoDeleteOnResume(t *testing.T) {
	provider := newScriptedProvider(
		// First call: handoff.
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "h1",
				Name:      "ask_human",
				Input:     json.RawMessage(`{"reason":"check","question":"Proceed?"}`),
			}},
		},
		// Second call (Resume): final answer.
		&ProviderResponse{Text: "Done."},
	)

	store := newTestHandoffStore()
	mem := newTestMemoryStore()

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{NewHandoffTool("ask_human", "")},
		WithHandoffStore(store),
		WithConversation(mem, "conv-2"),
	)
	if err != nil {
		t.Fatal(err)
	}

	c := Background()
	err = a.InvokeStream(c, "Do the thing", nil)
	if !errors.Is(err, ErrHandoffRequested) {
		t.Fatalf("expected ErrHandoffRequested, got %v", err)
	}

	hr, _ := GetHandoffRequest(c)
	_, err = a.ResumeInvoke(c, hr, "Yes, proceed")
	if err != nil {
		t.Fatalf("ResumeInvoke failed: %v", err)
	}

	// Should have been deleted after successful resume.
	if store.deleted != 1 {
		t.Fatalf("expected 1 delete after resume, got %d", store.deleted)
	}
	_, ok, _ := store.LoadHandoff(context.Background(), "conv-2")
	if ok {
		t.Error("expected HandoffRequest to be removed from store after Resume")
	}
}

// TestHandoffStore_NilStore verifies that when no HandoffStore is configured
// the agent still behaves correctly (backwards-compatible).
func TestHandoffStore_NilStore(t *testing.T) {
	provider := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "h1",
				Name:      "ask_human",
				Input:     json.RawMessage(`{"reason":"r","question":"q"}`),
			}},
		},
	)

	a, err := New(provider, prompt.Text("helpful"), []tool.Tool{NewHandoffTool("ask_human", "")})
	if err != nil {
		t.Fatal(err)
	}

	c := Background()
	err = a.InvokeStream(c, "hello", nil)
	if !errors.Is(err, ErrHandoffRequested) {
		t.Fatalf("expected ErrHandoffRequested without store, got %v", err)
	}
}
