package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestInvokeEndFiresOnEarlyLifecycleFailure(t *testing.T) {
	var starts, ends atomic.Int32
	a, err := New(mockProvider{}, "sys", WithHooks(Hooks{Invoke: func(ctx context.Context, r InvokeRecord) context.Context {
		if r.Phase == Start {
			starts.Add(1)
		} else {
			ends.Add(1)
		}
		return ctx
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background().WithExecutionID(""), "hi"); err == nil {
		t.Fatal("want error for empty execution ID")
	}
	a.random = errReader{}
	if _, err := a.Invoke(Background(), "hi"); err == nil {
		t.Fatal("want error for failing entropy source")
	}
	if starts.Load() != 2 || ends.Load() != 2 {
		t.Fatalf("starts=%d ends=%d, want 2/2", starts.Load(), ends.Load())
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestResumeTransientFailureKeepsPauseAndAnswer(t *testing.T) {
	provider := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "h-1", Name: "ask_review", Input: json.RawMessage(`{"reason":"r","question":"q"}`)}}},
		&ModelResponse{Text: "done"},
	)
	conversations, executions, stores := pauseStores()
	_ = conversations
	var guardCalls atomic.Int32
	guard := func(_ *Context, s string) (string, error) {
		if guardCalls.Add(1) == 2 { // first resume attempt fails transiently
			return "", errors.New("transient")
		}
		return s, nil
	}
	a, err := New(provider, "helpful", append(stores, WithTools(typedAskTool(nil)), WithInputGuardrail(guard))...)
	if err != nil {
		t.Fatal(err)
	}
	in := mustInterrupt(t, a, Background().WithConversationID("keep-pause"), "review")
	if _, err := a.Resume(Background(), in, RespondWith(reviewAnswer{Approved: true, Edited: "x"})); err == nil {
		t.Fatal("first resume should fail")
	}
	stored, err := executions.Load(context.Background(), in.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != ExecutionPaused || stored.Pause == nil || stored.Version != in.ExecutionVersion {
		t.Fatalf("after failed resume: status=%s pause=%v version=%d (want paused, same payload, version %d)", stored.Status, stored.Pause != nil, stored.Version, in.ExecutionVersion)
	}
	res, err := a.Resume(Background(), in, RespondWith(reviewAnswer{Approved: true, Edited: "x"}))
	if err != nil || res.Text != "done" {
		t.Fatalf("retry: res=%+v err=%v", res, err)
	}
}

func TestConversationLockWaitHonorsContext(t *testing.T) {
	var locks conversationLocks
	h := locks.acquire("c")
	defer h.release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := locks.acquireCtx(ctx, "c"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if locks.locksLen() != 1 {
		t.Fatalf("entry leaked: %d", locks.locksLen())
	}
}

func TestToolResultsCommittedAfterCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	slow := tool.NewRaw("slow", "slow", nil, func(context.Context, json.RawMessage) (string, error) {
		cancel() // the caller gives up while the handler finishes
		return "finished", nil
	})
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: batchOf("slow")}, {Text: "done"},
	}}
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	a, err := New(provider, "sys", WithTools(slow),
		WithConversationStore(ctxStrictConversation{conversations}),
		WithExecutionStore(ctxStrictExecution{executions}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(NewContext(parent).WithConversationID("cancel-commit"), "go")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	snap, err := conversations.Load(context.Background(), "cancel-commit")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range snap.Messages {
		for _, b := range m.Content {
			if r, ok := b.(ToolResultBlock); ok && r.Content == "finished" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("finished tool result was not committed: %+v", snap.Messages)
	}
	for _, e := range executions.items {
		if e.ToolBatch != nil {
			t.Fatalf("tool batch still active after commit: %+v", e.ToolBatch)
		}
	}
}

// ctxStrictConversation and ctxStrictExecution reject writes on a cancelled
// context, like a real network-backed store would.
type ctxStrictConversation struct{ *testMemoryStore }

func (s ctxStrictConversation) Append(ctx context.Context, id string, m []Message, rev uint64) (ConversationCursor, error) {
	if err := ctx.Err(); err != nil {
		return ConversationCursor{}, err
	}
	return s.testMemoryStore.Append(ctx, id, m, rev)
}

type ctxStrictExecution struct{ *testExecutionStore }

func (s ctxStrictExecution) Save(ctx context.Context, e Execution, v uint64) (Execution, error) {
	if err := ctx.Err(); err != nil {
		return Execution{}, err
	}
	return s.testExecutionStore.Save(ctx, e, v)
}
