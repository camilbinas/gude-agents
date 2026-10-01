package interruptstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func newStore() *Store { return New(checkpoint.NewMemory()) }

func sampleInterrupt() *agent.Interrupt {
	return &agent.Interrupt{
		ID:             "int-1",
		Type:           agent.InterruptHumanInput,
		ConversationID: "conv-1",
		Input: &agent.InputInterrupt{
			Reason:   "needs approval",
			Question: "Ship the release?",
		},
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "deploy please"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "checking"}}},
		},
	}
}

func sampleApproval() *agent.Interrupt {
	return &agent.Interrupt{
		ID:             "int-approval",
		Type:           agent.InterruptApproval,
		ConversationID: "conv-2",
		Approval: &agent.ApprovalInterrupt{Calls: []agent.ApprovalCall{
			{CallID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"A"}`)},
			{CallID: "tc-2", Name: "refund", Input: json.RawMessage(`{"amount":5}`)},
		}},
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "delete A"}}},
		},
	}
}

func TestSatisfiesInterruptStore(t *testing.T) {
	var _ agent.InterruptStore = newStore()
}

func TestAgentAcceptsStoreAsInterruptStore(t *testing.T) {
	a, err := agent.New(
		testutil.NewMockProvider(),
		"you are a test agent",
		agent.WithInterruptStore(newStore()),
	)
	if err != nil {
		t.Fatalf("agent.New with interrupt store: %v", err)
	}
	if a == nil {
		t.Fatal("agent is nil")
	}
}

func TestRoundTrip_HumanInputPreservesEveryField(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	want := sampleInterrupt()

	if err := s.Save(ctx, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load(ctx, want.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.ID != want.ID || got.Type != want.Type || got.ConversationID != want.ConversationID {
		t.Errorf("header = {%q %q %q}, want {%q %q %q}",
			got.ID, got.Type, got.ConversationID, want.ID, want.Type, want.ConversationID)
	}
	if !reflect.DeepEqual(got.Input, want.Input) {
		t.Errorf("Input = %+v, want %+v", got.Input, want.Input)
	}
	if got.Approval != nil {
		t.Errorf("Approval = %+v, want nil", got.Approval)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(got.Messages))
	}
}

func TestRoundTrip_ApprovalPreservesCalls(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	want := sampleApproval()

	if err := s.Save(ctx, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load(ctx, want.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Type != agent.InterruptApproval {
		t.Errorf("Type = %q, want approval", got.Type)
	}
	if !reflect.DeepEqual(got.Approval, want.Approval) {
		t.Errorf("Approval = %#v, want %#v", got.Approval, want.Approval)
	}
	if got.Input != nil {
		t.Errorf("Input = %+v, want nil", got.Input)
	}
}

// ContentBlock is a sealed interface, so it cannot round-trip through plain JSON.
// The store must use the type-discriminated conversation encoding — this asserts
// the concrete block type survives, not merely the message count.
func TestRoundTrip_ContentBlockTypesSurvive(t *testing.T) {
	s := newStore()
	ctx := context.Background()

	if err := s.Save(ctx, sampleInterrupt()); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load(ctx, "int-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	for i, wantText := range []string{"deploy please", "checking"} {
		if len(got.Messages[i].Content) != 1 {
			t.Fatalf("Messages[%d] has %d blocks, want 1", i, len(got.Messages[i].Content))
		}
		tb, ok := got.Messages[i].Content[0].(agent.TextBlock)
		if !ok {
			t.Fatalf("Messages[%d].Content[0] is %T, want agent.TextBlock", i, got.Messages[i].Content[0])
		}
		if tb.Text != wantText {
			t.Errorf("Messages[%d] text = %q, want %q", i, tb.Text, wantText)
		}
	}
	if got.Messages[0].Role != agent.RoleUser {
		t.Errorf("Messages[0].Role = %q, want user", got.Messages[0].Role)
	}
	if got.Messages[1].Role != agent.RoleAssistant {
		t.Errorf("Messages[1].Role = %q, want assistant", got.Messages[1].Role)
	}
}

// The interface requires an error wrapping agent.ErrInterruptNotFound for a
// missing interrupt.
func TestLoad_MissingReturnsErrInterruptNotFound(t *testing.T) {
	s := newStore()
	got, err := s.Load(context.Background(), "absent")
	if !errors.Is(err, agent.ErrInterruptNotFound) {
		t.Errorf("err = %v, want ErrInterruptNotFound", err)
	}
	if got != nil {
		t.Errorf("interrupt = %+v, want nil", got)
	}
}

func TestLoad_RejectsEmptyID(t *testing.T) {
	if _, err := newStore().Load(context.Background(), ""); err == nil {
		t.Error("expected error for empty ID, got nil")
	}
}

func TestClaim_ConsumesAndReturnsCanonicalInterrupt(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	want := sampleInterrupt()

	if err := s.Save(ctx, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := s.Load(ctx, want.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	loaded.Input.Question = "mutated after load"

	claimed, err := s.Claim(ctx, want.ID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Input.Question != want.Input.Question {
		t.Fatalf("claimed question = %q, want canonical %q", claimed.Input.Question, want.Input.Question)
	}
	if _, err := s.Load(ctx, want.ID); !errors.Is(err, agent.ErrInterruptNotFound) {
		t.Errorf("load after claim: err = %v, want ErrInterruptNotFound", err)
	}
	if _, err := s.Claim(ctx, want.ID); !errors.Is(err, agent.ErrInterruptNotFound) {
		t.Errorf("second claim: err = %v, want ErrInterruptNotFound", err)
	}
}

func TestClaim_UnknownInterruptReturnsNotFound(t *testing.T) {
	if _, err := newStore().Claim(context.Background(), "absent"); !errors.Is(err, agent.ErrInterruptNotFound) {
		t.Errorf("claim: err = %v, want ErrInterruptNotFound", err)
	}
}

func TestSave_RejectsEmptyID(t *testing.T) {
	in := sampleInterrupt()
	in.ID = ""
	if err := newStore().Save(context.Background(), in); err == nil {
		t.Error("expected error for empty interrupt ID, got nil")
	}
}

func TestSave_RejectsNil(t *testing.T) {
	if err := newStore().Save(context.Background(), nil); err == nil {
		t.Error("expected error for nil interrupt, got nil")
	}
}

// Save is create-only and must preserve the original pending record.
func TestSave_IsCreateOnly(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	first := sampleInterrupt()
	if err := s.Save(ctx, first); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second := sampleInterrupt()
	second.Input.Question = "replacement?"
	if err := s.Save(ctx, second); !errors.Is(err, checkpoint.ErrConflict) {
		t.Fatalf("second save err = %v, want ErrConflict", err)
	}
	got, err := s.Load(ctx, first.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Input.Question != first.Input.Question {
		t.Errorf("Question = %q, want original %q", got.Input.Question, first.Input.Question)
	}
}

// Claim appends a consumed tombstone so the pending payload remains in the
// checkpoint history for audit without remaining executable.
func TestClaim_PreservesPendingAndTombstoneHistory(t *testing.T) {
	cp := checkpoint.NewMemory()
	s := New(cp)
	ctx := context.Background()

	if err := s.Save(ctx, sampleInterrupt()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.Claim(ctx, "int-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	metas, err := cp.History(ctx, DefaultThreadPrefix+"int-1")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(metas))
	}
	if metas[0].Label != checkpointLabel || metas[1].Label != consumedCheckpointLabel {
		t.Errorf("labels = [%q %q], want [%q %q]", metas[0].Label, metas[1].Label, checkpointLabel, consumedCheckpointLabel)
	}
	pending, err := cp.LoadAt(ctx, DefaultThreadPrefix+"int-1", 1)
	if err != nil {
		t.Fatalf("load pending audit entry: %v", err)
	}
	if len(pending.Extra) == 0 {
		t.Fatal("pending audit payload was lost")
	}
	if _, err := s.Load(ctx, "int-1"); !errors.Is(err, agent.ErrInterruptNotFound) {
		t.Fatalf("load consumed interrupt: err = %v, want ErrInterruptNotFound", err)
	}
}

func TestInterruptsAreIsolated(t *testing.T) {
	s := newStore()
	ctx := context.Background()

	a := sampleInterrupt()
	a.ID = "int-a"
	a.Input.Question = "for a?"
	b := sampleInterrupt()
	b.ID = "int-b"
	b.Input.Question = "for b?"

	if err := s.Save(ctx, a); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := s.Save(ctx, b); err != nil {
		t.Fatalf("save b: %v", err)
	}

	got, err := s.Load(ctx, "int-a")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Input.Question != "for a?" {
		t.Errorf("Question = %q, want %q", got.Input.Question, "for a?")
	}

	if _, err := s.Claim(ctx, "int-a"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Load(ctx, "int-b"); err != nil {
		t.Errorf("claiming int-a affected int-b: %v", err)
	}
}

func TestClaim_HasExactlyOneCrossInstanceWinner(t *testing.T) {
	cp := checkpoint.NewMemory()
	stores := []*Store{New(cp), New(cp)}
	ctx := context.Background()
	if err := stores[0].Save(ctx, sampleInterrupt()); err != nil {
		t.Fatalf("save: %v", err)
	}

	const contenders = 32
	var winners atomic.Int32
	errs := make(chan error, contenders)
	var wg sync.WaitGroup
	wg.Add(contenders)
	for i := range contenders {
		go func(s *Store) {
			defer wg.Done()
			in, err := s.Claim(ctx, "int-1")
			if err == nil {
				if in == nil || in.ID != "int-1" {
					errs <- fmt.Errorf("winner returned interrupt %+v", in)
					return
				}
				winners.Add(1)
				return
			}
			if !errors.Is(err, agent.ErrInterruptNotFound) {
				errs <- err
			}
		}(stores[i%len(stores)])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("claim: %v", err)
	}
	if got := winners.Load(); got != 1 {
		t.Fatalf("winners = %d, want 1", got)
	}
}

func TestThreadPrefix_NamespacesAndIsConfigurable(t *testing.T) {
	cp := checkpoint.NewMemory()
	s := New(cp, WithThreadPrefix("is/"))
	ctx := context.Background()

	if err := s.Save(ctx, sampleInterrupt()); err != nil {
		t.Fatalf("save: %v", err)
	}
	ids, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 1 || ids[0] != "is/int-1" {
		t.Errorf("thread IDs = %v, want [is/int-1]", ids)
	}
}

// An interrupt store must not collide with other consumers of a shared backing store.
func TestDoesNotCollideWithOtherCheckpointConsumers(t *testing.T) {
	cp := checkpoint.NewMemory()
	s := New(cp)
	ctx := context.Background()

	// An unrelated consumer using the bare interrupt ID as its thread.
	if _, err := cp.Save(ctx, "int-1", checkpoint.Checkpoint{
		State: checkpoint.State{"unrelated": true},
	}); err != nil {
		t.Fatalf("foreign save: %v", err)
	}
	if err := s.Save(ctx, sampleInterrupt()); err != nil {
		t.Fatalf("save: %v", err)
	}

	foreign, err := cp.Load(ctx, "int-1")
	if err != nil {
		t.Fatalf("foreign load: %v", err)
	}
	if foreign.State["unrelated"] != true {
		t.Error("foreign checkpoint was overwritten")
	}

	if _, err := s.Claim(ctx, "int-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := cp.Load(ctx, "int-1"); errors.Is(err, checkpoint.ErrNotFound) {
		t.Error("claiming the interrupt also removed the foreign thread")
	}
}

// End to end: an approval interrupt is persisted by the agent, loaded back via
// LoadInterrupt (as another process would), atomically claimed, and resumed.
func TestAgentPersistsLoadsAndResumesInterrupt(t *testing.T) {
	var ran atomic.Bool
	deleteOrder := tool.NewRaw(
		"delete_order",
		"Permanently deletes an order",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
			"required":   []string{"order_id"},
		},
		func(context.Context, json.RawMessage) (string, error) {
			ran.Store(true)
			return `{"deleted":true}`, nil
		},
		tool.RequiresApproval(),
	)
	provider := testutil.NewMockProvider(testutil.WithResponses(
		&agent.ModelResponse{ToolCalls: []tool.Call{{
			ToolUseID: "tc-1", Name: "delete_order", Input: json.RawMessage(`{"order_id":"A"}`),
		}}},
		&agent.ModelResponse{Text: "deleted"},
	))
	store := newStore()
	a, err := agent.New(provider, "helpful",
		agent.WithTools(deleteOrder), agent.WithInterruptStore(store))
	if err != nil {
		t.Fatal(err)
	}

	res, err := a.Invoke(agent.Background(), "delete A")
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.StopReason != agent.StopInterrupt || res.Interrupt == nil {
		t.Fatalf("StopReason = %q, Interrupt = %v; want interrupt", res.StopReason, res.Interrupt)
	}
	if ran.Load() {
		t.Fatal("tool ran before approval")
	}

	loaded, err := a.LoadInterrupt(context.Background(), res.Interrupt.ID)
	if err != nil {
		t.Fatalf("LoadInterrupt: %v", err)
	}
	if loaded.Approval == nil || len(loaded.Approval.Calls) != 1 || loaded.Approval.Calls[0].CallID != "tc-1" {
		t.Fatalf("loaded approval = %+v", loaded.Approval)
	}
	if len(loaded.Messages) == 0 {
		t.Fatal("loaded interrupt has no resumable messages")
	}

	out, err := a.Resume(agent.Background(), loaded, agent.Approve())
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if out.Text != "deleted" {
		t.Errorf("Text = %q, want deleted", out.Text)
	}
	if !ran.Load() {
		t.Error("tool did not run after approval")
	}
	if _, err := store.Load(context.Background(), loaded.ID); !errors.Is(err, agent.ErrInterruptNotFound) {
		t.Errorf("after resume Load err = %v, want ErrInterruptNotFound", err)
	}
}

func TestNilCheckpointerReturnsErrors(t *testing.T) {
	s := New(nil)
	if err := s.Save(context.Background(), sampleInterrupt()); err == nil {
		t.Fatal("Save with nil checkpointer succeeded")
	}
	if _, err := s.Load(context.Background(), "int-1"); err == nil {
		t.Fatal("Load with nil checkpointer succeeded")
	}
	if _, err := s.Claim(context.Background(), "int-1"); err == nil {
		t.Fatal("Claim with nil checkpointer succeeded")
	}
}

func TestClaimRejectsEmptyID(t *testing.T) {
	if _, err := newStore().Claim(context.Background(), ""); err == nil {
		t.Fatal("Claim with empty ID succeeded")
	}
}

func TestLoadRejectsMismatchedStoredID(t *testing.T) {
	cp := checkpoint.NewMemory()
	s := New(cp)
	payload, err := conversation.MarshalInterrupt(sampleInterrupt())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Save(context.Background(), s.threadID("requested"), checkpoint.Checkpoint{Label: checkpointLabel, Extra: payload}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(context.Background(), "requested"); err == nil {
		t.Fatal("Load accepted a payload stored under the wrong ID")
	}
}
