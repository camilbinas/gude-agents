package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// testMemoryStore is a simple in-process Memory for testing.
type testMemoryStore struct {
	mu   sync.RWMutex
	data map[string]ConversationSnapshot
}

func newTestMemoryStore() *testMemoryStore {
	return &testMemoryStore{data: make(map[string]ConversationSnapshot)}
}

func (s *testMemoryStore) Load(_ context.Context, id string) (ConversationSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, ok := s.data[id]
	if !ok {
		return ConversationSnapshot{Messages: []Message{}}, nil
	}
	cp := make([]Message, len(snapshot.Messages))
	for i, m := range snapshot.Messages {
		content := make([]ContentBlock, len(m.Content))
		copy(content, m.Content)
		cp[i] = Message{Role: m.Role, Content: content}
	}
	return ConversationSnapshot{Messages: cp, Revision: snapshot.Revision}, nil
}

func (s *testMemoryStore) Save(_ context.Context, id string, msgs []Message, expectedRevision uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.data[id].Revision
	if current != expectedRevision {
		return 0, ErrConversationConflict
	}
	cp := make([]Message, len(msgs))
	for i, m := range msgs {
		content := make([]ContentBlock, len(m.Content))
		copy(content, m.Content)
		cp[i] = Message{Role: m.Role, Content: content}
	}
	next := current + 1
	s.data[id] = ConversationSnapshot{Messages: cp, Revision: next}
	return next, nil
}

func (s *testMemoryStore) List(_ context.Context) ([]string, error) { return nil, nil }
func (s *testMemoryStore) Delete(_ context.Context, _ string) error { return nil }

func TestAgent_LoadsHistoryOnSecondInvocation(t *testing.T) {
	sp := newScriptedProvider(
		&ModelResponse{Text: "first reply"},
		&ModelResponse{Text: "second reply"},
	)

	store := newTestMemoryStore()
	a, err := New(sp, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}

	result1, err := a.Invoke(Background().WithConversationID("conv-1"), "hello")
	if err != nil {
		t.Fatalf("first invoke: %v", err)
	}
	if result1.Text != "first reply" {
		t.Errorf("expected %q, got %q", "first reply", result1.Text)
	}

	result2, err := a.Invoke(Background().WithConversationID("conv-1"), "follow up")
	if err != nil {
		t.Fatalf("second invoke: %v", err)
	}
	if result2.Text != "second reply" {
		t.Errorf("expected %q, got %q", "second reply", result2.Text)
	}

	snapshot, err := store.Load(context.Background(), "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	saved := snapshot.Messages

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
	sp := newScriptedProvider(&ModelResponse{Text: "no memory response"})
	a, err := New(sp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background().WithConversationID("conv-1"), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "no memory response" {
		t.Errorf("expected %q, got %q", "no memory response", result.Text)
	}
}

type failingMemory struct{}

func (failingMemory) Load(_ context.Context, _ string) (ConversationSnapshot, error) {
	return ConversationSnapshot{}, fmt.Errorf("disk on fire")
}

func (failingMemory) Save(_ context.Context, _ string, _ []Message, _ uint64) (uint64, error) {
	return 0, nil
}

func (failingMemory) List(_ context.Context) ([]string, error) { return nil, nil }
func (failingMemory) Delete(_ context.Context, _ string) error { return nil }

func TestAgent_ConversationLoadFailureReturnsError(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "should not reach"})
	a, err := New(sp, "sys", WithConversationStore(failingMemory{}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background().WithConversationID("conv-1"), "hi")
	if err == nil {
		t.Fatal("expected error from memory load failure, got nil")
	}
	if !strings.Contains(err.Error(), "conversation load") {
		t.Errorf("expected error to contain 'conversation load', got: %v", err)
	}
}

// trackingFlusher implements ConversationStore and Flusher.
// It records whether Flush was called.
type trackingFlusher struct {
	flushed bool
	mu      sync.Mutex
	data    map[string]ConversationSnapshot
}

func newTrackingFlusher() *trackingFlusher {
	return &trackingFlusher{data: make(map[string]ConversationSnapshot)}
}

func (w *trackingFlusher) Load(_ context.Context, id string) (ConversationSnapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data[id], nil
}

func (w *trackingFlusher) Save(_ context.Context, id string, msgs []Message, expectedRevision uint64) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.data[id].Revision != expectedRevision {
		return 0, ErrConversationConflict
	}
	next := expectedRevision + 1
	w.data[id] = ConversationSnapshot{Messages: msgs, Revision: next}
	return next, nil
}

func (w *trackingFlusher) List(_ context.Context) ([]string, error) { return nil, nil }
func (w *trackingFlusher) Delete(_ context.Context, _ string) error { return nil }

func (w *trackingFlusher) Flush(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushed = true
	return nil
}

func TestAgent_ShutdownCallsConversationFlusher(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "ok"})
	flusher := newTrackingFlusher()

	a, err := New(sp, "sys", WithConversationStore(flusher))
	if err != nil {
		t.Fatal(err)
	}

	_ = a.Shutdown(context.Background())

	flusher.mu.Lock()
	defer flusher.mu.Unlock()
	if !flusher.flushed {
		t.Fatal("expected Shutdown to call Flush on Flusher")
	}
}

func TestAgent_Close_NoopWithoutConversation(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "ok"})
	a, err := New(sp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic.
	_ = a.Shutdown(context.Background())
	_ = a.Shutdown(context.Background()) // safe to call multiple times
}

func TestAgent_ShutdownNoopWhenConversationIsNotFlusher(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "ok"})
	store := newTestMemoryStore() // does not implement Flusher

	a, err := New(sp, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic — store doesn't implement Flush.
	_ = a.Shutdown(context.Background())
}

func testSaveLatest(ctx context.Context, store ConversationStore, id string, messages []Message) error {
	snapshot, err := store.Load(ctx, id)
	if err != nil {
		return err
	}
	_, err = store.Append(ctx, id, messages, snapshot.Revision)
	return err
}

func testLoadMessages(ctx context.Context, store ConversationStore, id string) ([]Message, error) {
	snapshot, err := store.Load(ctx, id)
	return snapshot.Messages, err
}
