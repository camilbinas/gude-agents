package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// inMemoryStore is a simple in-memory Conversation implementation for testing.
type inMemoryStore struct {
	data map[string][]Message
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{data: make(map[string][]Message)}
}

func (m *inMemoryStore) Load(_ context.Context, id string) (ConversationSnapshot, error) {
	return ConversationSnapshot{Messages: m.data[id]}, nil
}

func (m *inMemoryStore) Save(_ context.Context, id string, msgs []Message, expectedRevision uint64) (uint64, error) {
	m.data[id] = msgs
	return expectedRevision + 1, nil
}

func (m *inMemoryStore) List(_ context.Context) ([]string, error) { return nil, nil }
func (m *inMemoryStore) Delete(_ context.Context, _ string) error { return nil }

// TestConcurrentInvocations_DifferentConversations verifies that a single Agent
// instance can serve multiple concurrent conversations without cross-contamination.
// This is the core HTTP multi-tenancy requirement.
func TestConcurrentInvocations_DifferentConversations(t *testing.T) {
	// Each conversation gets its own scripted provider response.
	// We use a thread-safe provider that keys responses by conversation ID.
	var mu sync.Mutex
	callsByConv := map[string]int{}

	provider := &funcProvider{
		fn: func(ctx context.Context, params ModelRequest, cb func(ModelEvent)) (*ModelResponse, error) {
			// Extract the user message to identify which conversation this is.
			var userMsg string
			for _, m := range params.Messages {
				if m.Role == RoleUser {
					for _, b := range m.Content {
						if tb, ok := b.(TextBlock); ok {
							userMsg = tb.Text
						}
					}
				}
			}

			mu.Lock()
			callsByConv[userMsg]++
			mu.Unlock()

			return &ModelResponse{Text: "reply to: " + userMsg}, nil
		},
	}

	store := newTestMemoryStore()
	a, err := New(provider, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}

	// Launch 10 concurrent conversations.
	var wg sync.WaitGroup
	results := make([]string, 10)
	errs := make([]error, 10)

	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			convID := "conv-" + string(rune('A'+i))
			c := Background().WithConversationID(convID)
			res, err := a.Invoke(c, "msg-"+convID)
			results[i], errs[i] = res.Text, err
		}(i)
	}
	wg.Wait()

	// Verify no errors and each conversation got its own response.
	for i := range 10 {
		if errs[i] != nil {
			t.Errorf("conversation %d: unexpected error: %v", i, errs[i])
		}
		convID := "conv-" + string(rune('A'+i))
		expected := "reply to: msg-" + convID
		if results[i] != expected {
			t.Errorf("conversation %d: expected %q, got %q", i, expected, results[i])
		}
	}

	// Verify each conversation was saved to its own key.
	for i := range 10 {
		convID := "conv-" + string(rune('A'+i))
		msgs, _ := testLoadMessages(context.Background(), store, convID)
		if len(msgs) != 2 { // user + assistant
			t.Errorf("%s: expected 2 messages, got %d", convID, len(msgs))
		}
	}
}

// funcProvider is a test provider that delegates to a function.
type funcProvider struct {
	fn func(ctx context.Context, params ModelRequest, cb func(ModelEvent)) (*ModelResponse, error)
}

func (f *funcProvider) Name() string { return "mock" }

func (f *funcProvider) Stream(ctx context.Context, req ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	resp, err := f.fn(ctx, req, emit)
	if err != nil {
		return resp, err
	}
	// Stream text through callback like the real providers do.
	if emit != nil && resp.Text != "" && len(resp.ToolCalls) == 0 {
		emit(ModelEvent{Type: ModelEventText, Text: resp.Text})
	}
	return resp, nil
}

// TestMultiTurn_WithConversationStore verifies that one shared agent keeps
// conversations isolated by each invocation's explicit conversation ID.
func TestMultiTurn_WithConversationStore(t *testing.T) {
	callIndex := 0
	var mu sync.Mutex

	provider := &funcProvider{
		fn: func(ctx context.Context, params ModelRequest, cb func(ModelEvent)) (*ModelResponse, error) {
			mu.Lock()
			idx := callIndex
			callIndex++
			mu.Unlock()

			responses := []string{
				"Hello Alice",           // conv-1 turn 1
				"Hello Bob",             // conv-2 turn 1
				"I remember you, Alice", // conv-1 turn 2
				"I remember you, Bob",   // conv-2 turn 2
			}
			if idx < len(responses) {
				return &ModelResponse{Text: responses[idx]}, nil
			}
			return &ModelResponse{Text: "unexpected"}, nil
		},
	}

	store := newTestMemoryStore()
	a, err := New(provider, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}

	ctx1 := Background().WithConversationID("conv-1")
	ctx2 := Background().WithConversationID("conv-2")

	// Turn 1 for both conversations.
	r1, _ := a.Invoke(ctx1, "I'm Alice")
	r2, _ := a.Invoke(ctx2, "I'm Bob")

	if r1.Text != "Hello Alice" {
		t.Errorf("conv-1 turn 1: expected %q, got %q", "Hello Alice", r1.Text)
	}
	if r2.Text != "Hello Bob" {
		t.Errorf("conv-2 turn 1: expected %q, got %q", "Hello Bob", r2.Text)
	}

	// Turn 2 — each conversation should have its own history.
	r3, _ := a.Invoke(ctx1, "Who am I?")
	r4, _ := a.Invoke(ctx2, "Who am I?")

	if r3.Text != "I remember you, Alice" {
		t.Errorf("conv-1 turn 2: expected %q, got %q", "I remember you, Alice", r3.Text)
	}
	if r4.Text != "I remember you, Bob" {
		t.Errorf("conv-2 turn 2: expected %q, got %q", "I remember you, Bob", r4.Text)
	}

	// Verify conversation isolation: conv-1 has 4 messages, conv-2 has 4 messages.
	msgs1, _ := testLoadMessages(context.Background(), store, "conv-1")
	msgs2, _ := testLoadMessages(context.Background(), store, "conv-2")

	if len(msgs1) != 4 {
		t.Errorf("conv-1: expected 4 messages, got %d", len(msgs1))
	}
	if len(msgs2) != 4 {
		t.Errorf("conv-2: expected 4 messages, got %d", len(msgs2))
	}
}

// TestHandoff_WithPerInvocationConversationID verifies that handoff saves
// to the correct per-request conversation and Resume targets it.
func TestHandoff_WithPerInvocationConversationID(t *testing.T) {
	provider := newScriptedProvider(
		// First call: LLM triggers handoff.
		&ModelResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "h1",
				Name:      "request_human_input",
				Input:     json.RawMessage(`{"reason":"approval","question":"Approve?"}`),
			}},
		},
		// Second call (Resume): LLM responds.
		&ModelResponse{Text: "Approved and processed."},
	)

	store := newTestMemoryStore()
	a, err := New(provider, "sys", WithTools(NewHumanInputTool("request_human_input", "")),
		WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}

	c := Background().WithConversationID("user-42-session")
	first, err := a.Invoke(c, "Process refund")
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	hr := first.Interrupt
	if first.StopReason != StopInterrupt || hr == nil || hr.Type != InterruptHumanInput {
		t.Fatalf("expected human_input interrupt, got %+v", first)
	}

	// Verify the handoff captured the correct conversation ID.
	if hr.ConversationID != "user-42-session" {
		t.Errorf("handoff conversationID = %q, want %q", hr.ConversationID, "user-42-session")
	}

	// Verify messages were saved to the correct conversation key.
	saved, _ := testLoadMessages(context.Background(), store, "user-42-session")
	if len(saved) == 0 {
		t.Error("expected messages saved to user-42-session on handoff")
	}

	// Resume — should save to the same conversation.
	result, err := a.Resume(Background().WithConversationID("user-42-session"), hr, Respond("Yes, approved"))
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if result.Text != "Approved and processed." {
		t.Errorf("result = %q, want %q", result.Text, "Approved and processed.")
	}

	// Verify the resumed conversation was saved to the same key.
	saved, _ = testLoadMessages(context.Background(), store, "user-42-session")
	if len(saved) < 3 { // original msgs + human response + assistant response
		t.Errorf("expected at least 3 messages after resume, got %d", len(saved))
	}
}
