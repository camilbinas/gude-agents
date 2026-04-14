package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

// testMemoryStore is a simple in-process Memory for testing.
type testMemoryStore struct {
	mu   sync.RWMutex
	data map[string][]Message
}

func newTestMemoryStore() *testMemoryStore {
	return &testMemoryStore{data: make(map[string][]Message)}
}

func (s *testMemoryStore) Load(_ context.Context, id string) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := s.data[id]
	cp := make([]Message, len(msgs))
	for i, m := range msgs {
		content := make([]ContentBlock, len(m.Content))
		copy(content, m.Content)
		cp[i] = Message{Role: m.Role, Content: content}
	}
	return cp, nil
}

func (s *testMemoryStore) Save(_ context.Context, id string, msgs []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]Message, len(msgs))
	for i, m := range msgs {
		content := make([]ContentBlock, len(m.Content))
		copy(content, m.Content)
		cp[i] = Message{Role: m.Role, Content: content}
	}
	s.data[id] = cp
	return nil
}

func TestAgent_LoadsHistoryOnSecondInvocation(t *testing.T) {
	sp := newScriptedProvider(
		&ProviderResponse{Text: "first reply"},
		&ProviderResponse{Text: "second reply"},
	)

	store := newTestMemoryStore()
	a, err := New(sp, prompt.Text("sys"), nil, WithMemory(store, "conv-1"))
	if err != nil {
		t.Fatal(err)
	}

	result1, _, err := a.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("first invoke: %v", err)
	}
	if result1 != "first reply" {
		t.Errorf("expected %q, got %q", "first reply", result1)
	}

	result2, _, err := a.Invoke(context.Background(), "follow up")
	if err != nil {
		t.Fatalf("second invoke: %v", err)
	}
	if result2 != "second reply" {
		t.Errorf("expected %q, got %q", "second reply", result2)
	}

	saved, err := store.Load(context.Background(), "conv-1")
	if err != nil {
		t.Fatal(err)
	}

	if len(saved) != 4 {
		t.Fatalf("expected 4 messages in memory, got %d", len(saved))
	}

	expectations := []struct {
		role Role
		text string
	}{
		{RoleUser, "hello"},
		{RoleAssistant, "first reply"},
		{RoleUser, "follow up"},
		{RoleAssistant, "second reply"},
	}

	for i, exp := range expectations {
		if saved[i].Role != exp.role {
			t.Errorf("message[%d] role: expected %q, got %q", i, exp.role, saved[i].Role)
		}
		tb := saved[i].Content[0].(TextBlock)
		if tb.Text != exp.text {
			t.Errorf("message[%d] text: expected %q, got %q", i, exp.text, tb.Text)
		}
	}
}

func TestAgent_WorksWithoutMemory(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "no memory response"})
	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "no memory response" {
		t.Errorf("expected %q, got %q", "no memory response", result)
	}
}

type failingMemory struct{}

func (failingMemory) Load(_ context.Context, _ string) ([]Message, error) {
	return nil, fmt.Errorf("disk on fire")
}

func (failingMemory) Save(_ context.Context, _ string, _ []Message) error {
	return nil
}

func TestAgent_MemoryLoadFailureReturnsError(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "should not reach"})
	a, err := New(sp, prompt.Text("sys"), nil, WithMemory(failingMemory{}, "conv-1"))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error from memory load failure, got nil")
	}
	if !strings.Contains(err.Error(), "memory load") {
		t.Errorf("expected error to contain 'memory load', got: %v", err)
	}
}
