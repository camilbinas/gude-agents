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

// mockSwarmProvider is a simple mock that returns canned responses in sequence.
type mockSwarmProvider struct {
	responses []*ProviderResponse
	callIndex int
}

func (m *mockSwarmProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return m.nextResp(), nil
}

func (m *mockSwarmProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	resp := m.nextResp()
	if cb != nil && resp.Text != "" {
		cb(resp.Text)
	}
	return resp, nil
}

func (m *mockSwarmProvider) nextResp() *ProviderResponse {
	if m.callIndex >= len(m.responses) {
		return &ProviderResponse{Text: "no more responses"}
	}
	r := m.responses[m.callIndex]
	m.callIndex++
	return r
}

// inMemoryStore is a simple in-memory Memory implementation for testing.
type inMemoryStore struct {
	data map[string][]Message
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{data: make(map[string][]Message)}
}

func (m *inMemoryStore) Load(_ context.Context, id string) ([]Message, error) {
	return m.data[id], nil
}

func (m *inMemoryStore) Save(_ context.Context, id string, msgs []Message) error {
	m.data[id] = msgs
	return nil
}

// TestConcurrentInvocations_DifferentConversations verifies that a single Agent
// instance can serve multiple concurrent conversations without cross-contamination.
// This is the core HTTP multi-tenancy requirement.
func TestConcurrentInvocations_DifferentConversations(t *testing.T) {
	// Each conversation gets its own scripted provider response.
	// We use a thread-safe provider that keys responses by conversation ID.
	var mu sync.Mutex
	callsByConv := map[string]int{}

	provider := &funcProvider{
		fn: func(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
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

			return &ProviderResponse{Text: "reply to: " + userMsg}, nil
		},
	}

	store := newTestMemoryStore()
	a, err := New(provider, prompt.Text("sys"), nil, WithSharedConversation(store))
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
			ctx := WithConversationID(context.Background(), convID)
			results[i], _, errs[i] = a.Invoke(ctx, "msg-"+convID)
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
		msgs, _ := store.Load(context.Background(), convID)
		if len(msgs) != 2 { // user + assistant
			t.Errorf("%s: expected 2 messages, got %d", convID, len(msgs))
		}
	}
}

// funcProvider is a test provider that delegates to a function.
type funcProvider struct {
	fn func(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error)
}

func (f *funcProvider) Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error) {
	return f.fn(ctx, params, nil)
}

func (f *funcProvider) ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	resp, err := f.fn(ctx, params, cb)
	if err != nil {
		return resp, err
	}
	// Stream text through callback like the real providers do.
	if cb != nil && resp.Text != "" && len(resp.ToolCalls) == 0 {
		cb(resp.Text)
	}
	return resp, nil
}

// TestMultiTurn_WithSharedConversation verifies that multi-turn conversations work
// correctly when using WithSharedConversation with per-request conversation IDs.
func TestMultiTurn_WithSharedConversation(t *testing.T) {
	callIndex := 0
	var mu sync.Mutex

	provider := &funcProvider{
		fn: func(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
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
				return &ProviderResponse{Text: responses[idx]}, nil
			}
			return &ProviderResponse{Text: "unexpected"}, nil
		},
	}

	store := newTestMemoryStore()
	a, err := New(provider, prompt.Text("sys"), nil, WithSharedConversation(store))
	if err != nil {
		t.Fatal(err)
	}

	ctx1 := WithConversationID(context.Background(), "conv-1")
	ctx2 := WithConversationID(context.Background(), "conv-2")

	// Turn 1 for both conversations.
	r1, _, _ := a.Invoke(ctx1, "I'm Alice")
	r2, _, _ := a.Invoke(ctx2, "I'm Bob")

	if r1 != "Hello Alice" {
		t.Errorf("conv-1 turn 1: expected %q, got %q", "Hello Alice", r1)
	}
	if r2 != "Hello Bob" {
		t.Errorf("conv-2 turn 1: expected %q, got %q", "Hello Bob", r2)
	}

	// Turn 2 — each conversation should have its own history.
	r3, _, _ := a.Invoke(ctx1, "Who am I?")
	r4, _, _ := a.Invoke(ctx2, "Who am I?")

	if r3 != "I remember you, Alice" {
		t.Errorf("conv-1 turn 2: expected %q, got %q", "I remember you, Alice", r3)
	}
	if r4 != "I remember you, Bob" {
		t.Errorf("conv-2 turn 2: expected %q, got %q", "I remember you, Bob", r4)
	}

	// Verify conversation isolation: conv-1 has 4 messages, conv-2 has 4 messages.
	msgs1, _ := store.Load(context.Background(), "conv-1")
	msgs2, _ := store.Load(context.Background(), "conv-2")

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
		&ProviderResponse{
			ToolCalls: []tool.Call{{
				ToolUseID: "h1",
				Name:      "request_human_input",
				Input:     json.RawMessage(`{"reason":"approval","question":"Approve?"}`),
			}},
		},
		// Second call (Resume): LLM responds.
		&ProviderResponse{Text: "Approved and processed."},
	)

	store := newTestMemoryStore()
	a, err := New(provider, prompt.Text("sys"), []tool.Tool{NewHandoffTool("request_human_input", "")},
		WithSharedConversation(store))
	if err != nil {
		t.Fatal(err)
	}

	ic := NewInvocationContext()
	ctx := WithConversationID(context.Background(), "user-42-session")
	ctx = WithInvocationContext(ctx, ic)

	_, err = a.InvokeStream(ctx, "Process refund", nil)
	if !errors.Is(err, ErrHandoffRequested) {
		t.Fatalf("expected ErrHandoffRequested, got %v", err)
	}

	hr, ok := GetHandoffRequest(ic)
	if !ok {
		t.Fatal("expected HandoffRequest")
	}

	// Verify the handoff captured the correct conversation ID.
	if hr.ConversationID != "user-42-session" {
		t.Errorf("handoff conversationID = %q, want %q", hr.ConversationID, "user-42-session")
	}

	// Verify messages were saved to the correct conversation key.
	saved, _ := store.Load(context.Background(), "user-42-session")
	if len(saved) == 0 {
		t.Error("expected messages saved to user-42-session on handoff")
	}

	// Resume — should save to the same conversation.
	result, _, err := a.ResumeInvoke(context.Background(), hr, "Yes, approved")
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if result != "Approved and processed." {
		t.Errorf("result = %q, want %q", result, "Approved and processed.")
	}

	// Verify the resumed conversation was saved to the same key.
	saved, _ = store.Load(context.Background(), "user-42-session")
	if len(saved) < 3 { // original msgs + human response + assistant response
		t.Errorf("expected at least 3 messages after resume, got %d", len(saved))
	}
}

// TestSwarm_WithPerInvocationConversationID verifies that a Swarm can serve
// different conversations via context-based conversation IDs.
func TestSwarm_WithPerInvocationConversationID(t *testing.T) {
	alphaProvider := &mockSwarmProvider{responses: []*ProviderResponse{
		{Text: "Alpha reply for conv-X"},
		{Text: "Alpha reply for conv-Y"},
	}}

	a1, _ := New(alphaProvider, prompt.Text("alpha"), nil)
	a2, _ := New(&mockSwarmProvider{}, prompt.Text("beta"), nil)

	mem := newInMemoryStore()
	sw, _ := NewSwarm([]SwarmMember{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	}, WithSwarmConversation(mem, "default"))

	// Two different conversations on the same swarm.
	ctxX := WithConversationID(context.Background(), "conv-X")
	ctxY := WithConversationID(context.Background(), "conv-Y")

	rX, err := sw.Invoke(ctxX, "hello X")
	if err != nil {
		t.Fatal(err)
	}
	rY, err := sw.Invoke(ctxY, "hello Y")
	if err != nil {
		t.Fatal(err)
	}

	if rX.Response != "Alpha reply for conv-X" {
		t.Errorf("conv-X: expected %q, got %q", "Alpha reply for conv-X", rX.Response)
	}
	if rY.Response != "Alpha reply for conv-Y" {
		t.Errorf("conv-Y: expected %q, got %q", "Alpha reply for conv-Y", rY.Response)
	}

	// Verify conversations were saved separately.
	msgsX := mem.data["conv-X"]
	msgsY := mem.data["conv-Y"]
	msgsDefault := mem.data["default"]

	if len(msgsX) != 2 {
		t.Errorf("conv-X: expected 2 messages, got %d", len(msgsX))
	}
	if len(msgsY) != 2 {
		t.Errorf("conv-Y: expected 2 messages, got %d", len(msgsY))
	}
	if len(msgsDefault) != 0 {
		t.Errorf("default: expected 0 messages, got %d", len(msgsDefault))
	}
}
