package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/rag"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// countingProvider counts Stream calls and always answers with text.
type countingProvider struct {
	calls atomic.Int32
	resp  *ModelResponse
}

func (p *countingProvider) Name() string { return "counting" }

func (p *countingProvider) Stream(_ context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	p.calls.Add(1)
	if p.resp != nil {
		return p.resp, nil
	}
	if emit != nil {
		emit(ModelEvent{Type: ModelEventText, Text: "reply"})
	}
	return &ModelResponse{Text: "reply"}, nil
}

// missingIDFixture is a stateful Agent wired with every kind of invocation
// work, each instrumented so tests can prove none of it ran.
type missingIDFixture struct {
	agent      *Agent
	provider   *countingProvider
	store      *failingSaveConversation
	retriever  *countingRetriever
	guardCalls atomic.Int32
	toolCalls  atomic.Int32
}

func newMissingIDFixture(t *testing.T) *missingIDFixture {
	t.Helper()
	f := &missingIDFixture{
		// The provider would call the tool if it were ever reached.
		provider: &countingProvider{resp: &ModelResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc-1", Name: "side_effect", Input: json.RawMessage(`{}`)},
		}}},
		store:     &failingSaveConversation{},
		retriever: &countingRetriever{docs: []rag.Document{{Content: "doc"}}},
	}
	sideEffect := tool.NewRaw("side_effect", "external side effect",
		func(context.Context, json.RawMessage) (string, error) {
			f.toolCalls.Add(1)
			return "done", nil
		}, tool.WithSchema(map[string]any{"type": "object"}))
	a, err := New(f.provider, "sys",
		WithConversationStore(f.store),
		WithSyncConversation(),
		WithRetriever(f.retriever),
		WithTools(sideEffect),
		WithInputGuardrail(func(_ *Context, msg string) (string, error) {
			f.guardCalls.Add(1)
			return msg, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.agent = a
	return f
}

// assertNoWork verifies that no invocation work happened.
func (f *missingIDFixture) assertNoWork(t *testing.T) {
	t.Helper()
	if n := f.provider.calls.Load(); n != 0 {
		t.Errorf("provider called %d times", n)
	}
	if n := f.retriever.callCount(); n != 0 {
		t.Errorf("retriever called %d times", n)
	}
	if n := f.toolCalls.Load(); n != 0 {
		t.Errorf("tool called %d times", n)
	}
	if n := f.guardCalls.Load(); n != 0 {
		t.Errorf("input guardrail called %d times", n)
	}
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	if f.store.loads != 0 || f.store.saves != 0 || f.store.flushes != 0 {
		t.Errorf("store touched: loads=%d saves=%d flushes=%d", f.store.loads, f.store.saves, f.store.flushes)
	}
}

func TestConversationID_NoStoreNoIDIsStateless(t *testing.T) {
	p := &countingProvider{}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "reply" || p.calls.Load() != 1 {
		t.Fatalf("text=%q provider calls=%d", res.Text, p.calls.Load())
	}
}

func TestConversationID_NoStoreWithIDIsValid(t *testing.T) {
	a, err := New(&countingProvider{}, "sys")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background().WithConversationID("thread-123"), "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestConversationID_StoreWithIDLoadsAndSaves(t *testing.T) {
	store := &failingSaveConversation{}
	a, err := New(&countingProvider{}, "sys", WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background().WithConversationID("thread-123"), "hello"); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loads != 1 || store.saves != 1 {
		t.Fatalf("loads=%d saves=%d, want 1 and 1", store.loads, store.saves)
	}
	if len(store.snapshot.Messages) != 2 {
		t.Fatalf("persisted %d messages, want 2", len(store.snapshot.Messages))
	}
}

func TestConversationID_StoreMissingIDInvokeFailsWithoutWork(t *testing.T) {
	for name, ctx := range map[string]*Context{
		"Background":               Background(),
		"explicit empty ID":        Background().WithConversationID(""),
		"NewContext without an ID": NewContext(context.Background()),
	} {
		t.Run(name, func(t *testing.T) {
			f := newMissingIDFixture(t)
			_, err := f.agent.Invoke(ctx, "hello")
			if !errors.Is(err, ErrConversationIDRequired) {
				t.Fatalf("err = %v, want ErrConversationIDRequired", err)
			}
			f.assertNoWork(t)
		})
	}
}

func TestConversationID_StoreMissingIDStreamEndsWithError(t *testing.T) {
	f := newMissingIDFixture(t)
	var (
		end     *Event
		lastErr error
	)
	for ev, err := range f.agent.Stream(Background(), "hello") {
		if ev.Type == EventEnd {
			e := ev
			end = &e
		}
		if err != nil {
			lastErr = err
		}
	}
	if !errors.Is(lastErr, ErrConversationIDRequired) {
		t.Fatalf("stream err = %v, want ErrConversationIDRequired", lastErr)
	}
	if end == nil || end.Error == nil {
		t.Fatalf("stream did not terminate with an EventEnd carrying the error: %+v", end)
	}
	f.assertNoWork(t)
}

func TestConversationID_StoreMissingIDTextStreamPropagatesError(t *testing.T) {
	f := newMissingIDFixture(t)
	var (
		chunks  int
		lastErr error
	)
	for chunk, err := range f.agent.TextStream(Background(), "hello") {
		if err != nil {
			lastErr = err
			continue
		}
		if chunk != "" {
			chunks++
		}
	}
	if !errors.Is(lastErr, ErrConversationIDRequired) {
		t.Fatalf("text stream err = %v, want ErrConversationIDRequired", lastErr)
	}
	if chunks != 0 {
		t.Fatalf("text stream yielded %d chunks", chunks)
	}
	f.assertNoWork(t)
}

func TestConversationID_StoreMissingIDInvokeSchemaFails(t *testing.T) {
	f := newMissingIDFixture(t)
	decoded := false
	_, err := f.agent.InvokeSchema(Background(), "hello", map[string]any{"type": "object"},
		func(json.RawMessage) error { decoded = true; return nil })
	if !errors.Is(err, ErrConversationIDRequired) {
		t.Fatalf("err = %v, want ErrConversationIDRequired", err)
	}
	if decoded {
		t.Fatal("decoder ran")
	}
	f.assertNoWork(t)
}

// TestConversationID_StoreMissingIDPreventsBackgroundDispatch verifies a
// background-capable Agent cannot dispatch work without a conversation ID.
func TestConversationID_StoreMissingIDPreventsBackgroundDispatch(t *testing.T) {
	var handlerCalls atomic.Int32
	bg := newTestBackgroundRaw("bg", "background work", "started", map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (string, error) {
			handlerCalls.Add(1)
			return "done", nil
		})
	p := &countingProvider{resp: &ModelResponse{ToolCalls: []tool.Call{
		{ToolUseID: "tc-bg", Name: "bg", Input: json.RawMessage(`{}`)},
	}}}
	a, err := New(p, "sys", WithTools(bg), WithConversationStore(newMemConversation()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "go"); !errors.Is(err, ErrConversationIDRequired) {
		t.Fatalf("err = %v, want ErrConversationIDRequired", err)
	}
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if handlerCalls.Load() != 0 || p.calls.Load() != 0 {
		t.Fatalf("handler calls=%d provider calls=%d, want 0", handlerCalls.Load(), p.calls.Load())
	}
}

// TestResume_UnboundInterruptRejectedByStatefulAgent verifies that an
// interrupt without a conversation ID cannot be resumed through an Agent with
// a ConversationStore, that the resume context's ID is not used as a
// substitute, and that the interrupt is not consumed.
func TestResume_UnboundInterruptRejectedByStatefulAgent(t *testing.T) {
	interrupts := newTestInterruptStore()

	// A stateless Agent legitimately produces an interrupt without an ID.
	stateless, err := New(newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"A"}`)}}},
	), "helpful", WithTools(deleteOrderTool()), WithInterruptStore(interrupts))
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, stateless, Background(), "delete A")
	if in.ConversationID != "" {
		t.Fatalf("interrupt ConversationID = %q, want empty", in.ConversationID)
	}

	conv := newMemConversation()
	seeded := []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "other conversation"}}}}
	if _, err := conv.Save(context.Background(), "other", seeded, 0); err != nil {
		t.Fatal(err)
	}
	p := &countingProvider{}
	stateful, err := New(p, "helpful", WithTools(deleteOrderTool()),
		WithConversationStore(conv), WithInterruptStore(interrupts))
	if err != nil {
		t.Fatal(err)
	}

	_, err = stateful.Resume(Background().WithConversationID("other"), in, Approve())
	if !errors.Is(err, ErrConversationIDRequired) {
		t.Fatalf("err = %v, want ErrConversationIDRequired", err)
	}
	if p.calls.Load() != 0 {
		t.Fatalf("provider called %d times", p.calls.Load())
	}
	interrupts.mu.Lock()
	claimed := len(interrupts.claimed)
	interrupts.mu.Unlock()
	if claimed != 0 {
		t.Fatal("rejected resume consumed the interrupt")
	}
	if _, err := stateful.LoadInterrupt(context.Background(), in.ID); err != nil {
		t.Fatalf("interrupt no longer loadable: %v", err)
	}
	snapshot, err := conv.Load(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 1 || len(snapshot.Messages) != 1 {
		t.Fatalf("resume touched the context's conversation: %+v", snapshot)
	}
}
