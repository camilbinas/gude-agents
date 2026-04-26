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
	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(store, "conv-1"))
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

func TestAgent_WorksWithoutConversation(t *testing.T) {
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

func TestAgent_ConversationLoadFailureReturnsError(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "should not reach"})
	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(failingMemory{}, "conv-1"))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error from memory load failure, got nil")
	}
	if !strings.Contains(err.Error(), "conversation load") {
		t.Errorf("expected error to contain 'conversation load', got: %v", err)
	}
}

// trackingWaiter implements Conversation and ConversationWaiter.
// It records whether Wait was called.
type trackingWaiter struct {
	waited bool
	mu     sync.Mutex
	data   map[string][]Message
}

func newTrackingWaiter() *trackingWaiter {
	return &trackingWaiter{data: make(map[string][]Message)}
}

func (w *trackingWaiter) Load(_ context.Context, id string) ([]Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data[id], nil
}

func (w *trackingWaiter) Save(_ context.Context, id string, msgs []Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data[id] = msgs
	return nil
}

func (w *trackingWaiter) Wait() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.waited = true
}

func TestAgent_Close_CallsConversationWaiter(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "ok"})
	waiter := newTrackingWaiter()

	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(waiter, "conv-1"))
	if err != nil {
		t.Fatal(err)
	}

	a.Close()

	waiter.mu.Lock()
	defer waiter.mu.Unlock()
	if !waiter.waited {
		t.Fatal("expected Close to call Wait on ConversationWaiter")
	}
}

func TestAgent_Close_NoopWithoutConversation(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "ok"})
	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic.
	a.Close()
	a.Close() // safe to call multiple times
}

func TestAgent_Close_NoopWhenConversationIsNotWaiter(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "ok"})
	store := newTestMemoryStore() // does not implement ConversationWaiter

	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(store, "conv-1"))
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic — store doesn't implement Wait.
	a.Close()
}
