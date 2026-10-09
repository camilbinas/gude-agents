package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

func TestEngineAppendsOnlyNewTurnMessages(t *testing.T) {
	store := newTestMemoryStore()
	provider := newScriptedProvider(&ModelResponse{Text: "one"}, &ModelResponse{Text: "two"})
	a, err := New(provider, "system", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("c")
	if _, err := a.Invoke(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 2 || len(snapshot.Messages) != 4 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	for i, want := range []string{"first", "one", "second", "two"} {
		if got := snapshot.Messages[i].Content[0].(TextBlock).Text; got != want {
			t.Fatalf("message %d=%q,want %q", i, got, want)
		}
	}
}

func TestPersistentExecutionStoresCursorNotTranscript(t *testing.T) {
	approval := tool.NewRaw("approve", "approval", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil }, tool.RequiresApproval())
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call", Name: "approve", Input: json.RawMessage(`{}`)}}}, &ModelResponse{Text: "done"})
	store := newTestMemoryStore()
	executions := newTestExecutionStore()
	a, err := New(provider, "system", WithConversationStore(store), WithExecutionStore(executions), WithTools(approval))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("c")
	paused, err := a.Invoke(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	if paused.Interrupt == nil || paused.Interrupt.LastSequence == 0 || len(paused.Interrupt.Messages) != 0 {
		t.Fatalf("interrupt=%+v", paused.Interrupt)
	}
	if _, err := a.Resume(ctx, paused.Interrupt, Approve()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := store.Load(ctx, "c")
	if len(snapshot.Messages) < 4 {
		t.Fatalf("resume did not append canonical events: %+v", snapshot)
	}
}

type rangeResumeStore struct {
	mu             sync.Mutex
	messages       []Message
	revision       uint64
	lastSequence   uint64
	loadCalls      int
	loadAfterCalls []uint64
	ids            []string
}

func (s *rangeResumeStore) Load(_ context.Context, id string) (ConversationSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadCalls++
	s.ids = append(s.ids, id)
	return ConversationSnapshot{Messages: append([]Message(nil), s.messages...), Revision: s.revision, LastSequence: s.lastSequence}, nil
}

func (s *rangeResumeStore) LoadAfter(_ context.Context, id string, after uint64) (ConversationSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadAfterCalls = append(s.loadAfterCalls, after)
	s.ids = append(s.ids, id)
	start := int(after)
	if start > len(s.messages) {
		start = len(s.messages)
	}
	return ConversationSnapshot{Messages: append([]Message(nil), s.messages[start:]...), Revision: s.revision, LastSequence: s.lastSequence}, nil
}

func (s *rangeResumeStore) Append(_ context.Context, id string, messages []Message, expected uint64) (ConversationCursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, id)
	if expected != s.revision {
		return ConversationCursor{}, ErrConversationConflict
	}
	if len(messages) == 0 {
		return ConversationCursor{Revision: s.revision, LastSequence: s.lastSequence}, nil
	}
	s.messages = append(s.messages, messages...)
	s.revision++
	s.lastSequence += uint64(len(messages))
	return ConversationCursor{Revision: s.revision, LastSequence: s.lastSequence}, nil
}

type rangeResumeManager struct {
	boundary uint64
	calls    int
}

func (m *rangeResumeManager) HistoryBoundary(context.Context, string) (uint64, error) {
	m.calls++
	return m.boundary, nil
}
func (m *rangeResumeManager) Prepare(_ context.Context, in ContextManagerInput) (ContextManagerOutput, error) {
	return ContextManagerOutput{Messages: append(append([]Message(nil), in.Recent...), in.Current...)}, nil
}

func TestResumeUsesContextManagerRangeHistory(t *testing.T) {
	store := &rangeResumeStore{
		messages: []Message{
			{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "old user"}}},
			{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "old assistant"}}},
			{Role: RoleAssistant, Content: []ContentBlock{ToolUseBlock{ToolUseID: "old-tool", Name: "old", Input: json.RawMessage(`{}`)}}},
			{Role: RoleUser, Content: []ContentBlock{ToolResultBlock{ToolUseID: "old-tool", Content: "old result"}}},
		},
		revision:     1,
		lastSequence: 4,
	}
	manager := &rangeResumeManager{boundary: 2}
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "approve-1", Name: "approve", Input: json.RawMessage(`{}`)}}},
		{Text: "resumed"},
	}}
	called := false
	approval := tool.NewRaw("approve", "approve", nil, func(context.Context, json.RawMessage) (string, error) {
		called = true
		return "approved", nil
	}, tool.RequiresApproval())
	a, err := New(provider, "sys", WithTools(approval), WithConversationStore(store), WithContextManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	ctx := Background().WithConversationID("conversation")
	in := mustInterrupt(t, a, ctx, "pause")
	if in.LastSequence != 6 {
		t.Fatalf("interrupt LastSequence = %d, want 6", in.LastSequence)
	}

	result, err := a.Resume(ctx, in, Approve())
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "resumed" || !called {
		t.Fatalf("result=%+v called=%v", result, called)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loadCalls != 0 {
		t.Fatalf("resume used Load %d time(s)", store.loadCalls)
	}
	if len(store.loadAfterCalls) < 2 || store.loadAfterCalls[len(store.loadAfterCalls)-1] != 2 {
		t.Fatalf("LoadAfter boundaries = %v, want resume boundary 2", store.loadAfterCalls)
	}
	if manager.calls < 2 {
		t.Fatalf("HistoryBoundary calls = %d, want fresh + resume", manager.calls)
	}
}

func TestResumeRangeHistoryRetainsLastSequenceConflict(t *testing.T) {
	store := &rangeResumeStore{revision: 1, lastSequence: 3}
	manager := &rangeResumeManager{boundary: 2}
	provider := newScriptedProvider()
	a, err := New(provider, "sys", WithConversationStore(store), WithContextManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	r := &run{a: a, c: Background().WithConversationID("conversation"), convID: "conversation"}
	in := &Interrupt{Type: InterruptHumanInput, Revision: 1, LastSequence: 2}
	_, err = r.resumeTurn(in, Respond("answer"))
	if err == nil || !errors.Is(err, ErrConversationConflict) {
		t.Fatalf("resume error = %v, want ErrConversationConflict", err)
	}
}

// adversarialProvider returns randomized ModelResponses designed to stress
// conversation persistence invariants. It can return:
// - Empty text with no tool calls
// - Text-only responses (normal)
// - Tool calls with empty text (normal)
// - Tool calls with non-empty text (normal)
// - Whitespace-only text
type adversarialProvider struct {
	responses []*ModelResponse
	idx       int
}

func (p *adversarialProvider) Name() string { return "adversarial" }

func (p *adversarialProvider) next() *ModelResponse {
	if p.idx >= len(p.responses) {
		// Fallback: return text to terminate the loop.
		return &ModelResponse{Text: "fallback"}
	}
	r := p.responses[p.idx]
	p.idx++
	return r
}

func (p *adversarialProvider) Stream(_ context.Context, _ ModelRequest, cb func(ModelEvent)) (*ModelResponse, error) {
	r := p.next()
	if cb != nil && r.Text != "" && len(r.ToolCalls) == 0 {
		cb(ModelEvent{Type: ModelEventText, Text: r.Text})
	}
	return r, nil
}

// trackingConversation records all messages passed to Save.
type trackingConversation struct {
	saved [][]Message
}

func (t *trackingConversation) Load(_ context.Context, _ string) (ConversationSnapshot, error) {
	return ConversationSnapshot{Messages: []Message{}}, nil
}

func (t *trackingConversation) Save(_ context.Context, _ string, msgs []Message, expectedRevision uint64) (uint64, error) {
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	t.saved = append(t.saved, cp)
	return expectedRevision + 1, nil
}

func (t *trackingConversation) List(_ context.Context) ([]string, error) { return nil, nil }
func (t *trackingConversation) Delete(_ context.Context, _ string) error { return nil }

func (t *trackingConversation) lastSaved() []Message {
	if len(t.saved) == 0 {
		return nil
	}
	return t.saved[len(t.saved)-1]
}

// genAdversarialResponses generates a sequence of ModelResponses that
// exercises edge cases: empty text, whitespace text, tool calls with/without
// text, and eventually a terminating response.
func genAdversarialResponses(rt *rapid.T, toolNames []string) []*ModelResponse {
	// Generate 1-4 tool-call iterations followed by a final response.
	numToolIters := rapid.IntRange(0, 4).Draw(rt, "numToolIters")
	responses := make([]*ModelResponse, 0, numToolIters+1)

	for i := range numToolIters {
		numCalls := rapid.IntRange(1, 3).Draw(rt, fmt.Sprintf("numCalls_%d", i))
		calls := make([]tool.Call, numCalls)
		for j := range numCalls {
			calls[j] = tool.Call{
				ToolUseID: fmt.Sprintf("tc-%d-%d", i, j),
				Name:      toolNames[j%len(toolNames)],
				Input:     json.RawMessage(`{}`),
			}
		}
		// Randomly include text alongside tool calls.
		text := ""
		if rapid.Bool().Draw(rt, fmt.Sprintf("hasText_%d", i)) {
			text = rapid.OneOf(
				rapid.Just(""),
				rapid.Just("   "),
				rapid.Just("\n"),
				rapid.StringMatching(`[a-zA-Z ]{1,30}`),
			).Draw(rt, fmt.Sprintf("toolText_%d", i))
		}
		responses = append(responses, &ModelResponse{
			Text:      text,
			ToolCalls: calls,
			Usage:     TokenUsage{InputTokens: 10, OutputTokens: 5},
		})
	}

	// Final response — may be empty, whitespace, or normal text.
	finalText := rapid.OneOf(
		rapid.Just(""),
		rapid.Just("   "),
		rapid.Just("\t\n"),
		rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,100}`),
	).Draw(rt, "finalText")
	responses = append(responses, &ModelResponse{
		Text:  finalText,
		Usage: TokenUsage{InputTokens: 10, OutputTokens: 5},
	})

	return responses
}

// TestProperty_ConversationNoEmptyTextBlocks verifies that for any sequence of
// adversarial provider responses, the saved conversation never contains a
// TextBlock with empty text.
func TestProperty_ConversationNoEmptyTextBlocks(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolNames := []string{"tool_a", "tool_b", "tool_c"}
		tools := make([]tool.Tool, len(toolNames))
		for i, name := range toolNames {
			tools[i] = newTestRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		responses := genAdversarialResponses(rt, toolNames)
		provider := &adversarialProvider{responses: responses}
		store := &trackingConversation{}

		a, err := New(provider, "test", WithTools(tools...),
			WithConversationStore(store),
			WithMaxIterations(10),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		// Run the agent — we don't care about the result, only the saved conversation.
		a.Invoke(Background().WithConversationID("test-conv"), "hello")

		msgs := store.lastSaved()
		for i, msg := range msgs {
			for j, block := range msg.Content {
				if tb, ok := block.(TextBlock); ok && tb.Text == "" {
					rt.Fatalf("empty TextBlock at message[%d].Content[%d] (role=%s)", i, j, msg.Role)
				}
			}
		}
	})
}

// TestProperty_ConversationNoEmptyContentMessages verifies that no saved message
// has an empty Content slice.
func TestProperty_ConversationNoEmptyContentMessages(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolNames := []string{"tool_a", "tool_b"}
		tools := make([]tool.Tool, len(toolNames))
		for i, name := range toolNames {
			tools[i] = newTestRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		responses := genAdversarialResponses(rt, toolNames)
		provider := &adversarialProvider{responses: responses}
		store := &trackingConversation{}

		a, err := New(provider, "test", WithTools(tools...),
			WithConversationStore(store),
			WithMaxIterations(10),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		a.Invoke(Background().WithConversationID("test-conv"), "hello")

		msgs := store.lastSaved()
		for i, msg := range msgs {
			if len(msg.Content) == 0 {
				rt.Fatalf("empty Content at message[%d] (role=%s)", i, msg.Role)
			}
		}
	})
}

// TestProperty_ConversationToolResultsHaveMatchingToolUse verifies that every
// ToolResultBlock in the saved conversation has a corresponding ToolUseBlock
// earlier in the conversation.
func TestProperty_ConversationToolResultsHaveMatchingToolUse(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolNames := []string{"tool_a", "tool_b", "tool_c"}
		tools := make([]tool.Tool, len(toolNames))
		for i, name := range toolNames {
			tools[i] = newTestRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		responses := genAdversarialResponses(rt, toolNames)
		provider := &adversarialProvider{responses: responses}
		store := &trackingConversation{}

		a, err := New(provider, "test", WithTools(tools...),
			WithConversationStore(store),
			WithMaxIterations(10),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		a.Invoke(Background().WithConversationID("test-conv"), "hello")

		msgs := store.lastSaved()

		// Collect all ToolUseBlock IDs.
		toolUseIDs := make(map[string]bool)
		for _, msg := range msgs {
			for _, block := range msg.Content {
				if tu, ok := block.(ToolUseBlock); ok {
					toolUseIDs[tu.ToolUseID] = true
				}
			}
		}

		// Verify every ToolResultBlock references a known ToolUseBlock.
		for i, msg := range msgs {
			for j, block := range msg.Content {
				if tr, ok := block.(ToolResultBlock); ok {
					if !toolUseIDs[tr.ToolUseID] {
						rt.Fatalf("orphaned ToolResultBlock at message[%d].Content[%d]: ToolUseID=%q not found", i, j, tr.ToolUseID)
					}
				}
			}
		}
	})
}

// TestProperty_ConversationRolesAlternate verifies that saved messages alternate
// between user and assistant roles (with the exception that the first message
// may be either role depending on RAG injection).
func TestProperty_ConversationRolesAlternate(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolNames := []string{"tool_a"}
		tools := make([]tool.Tool, len(toolNames))
		for i, name := range toolNames {
			tools[i] = newTestRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		responses := genAdversarialResponses(rt, toolNames)
		provider := &adversarialProvider{responses: responses}
		store := &trackingConversation{}

		a, err := New(provider, "test", WithTools(tools...),
			WithConversationStore(store),
			WithMaxIterations(10),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		a.Invoke(Background().WithConversationID("test-conv"), "hello")

		msgs := store.lastSaved()
		if len(msgs) < 2 {
			return // too few messages to check alternation
		}

		for i := 1; i < len(msgs); i++ {
			if msgs[i].Role == msgs[i-1].Role {
				rt.Fatalf("consecutive same-role messages at [%d] and [%d]: both %s", i-1, i, msgs[i].Role)
			}
		}
	})
}

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
