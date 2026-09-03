package handoffstore

import (
	"context"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
)

func newStore() *Store { return New(checkpoint.NewMemory()) }

func sampleRequest() *agent.HandoffRequest {
	return &agent.HandoffRequest{
		Reason:         "needs approval",
		Question:       "Ship the release?",
		ConversationID: "conv-1",
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "deploy please"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "checking"}}},
		},
	}
}

func TestSatisfiesHandoffStore(t *testing.T) {
	var _ agent.HandoffStore = newStore()
}

// The payoff of the extraction: agent.HandoffStore had no implementation anywhere
// in the repo, and now a checkpoint backend supplies one that agent.New accepts.
func TestAgentAcceptsStoreAsHandoffStore(t *testing.T) {
	a, err := agent.New(
		testutil.NewMockProvider(),
		prompt.Text("you are a test agent"),
		nil,
		agent.WithHandoffStore(newStore()),
	)
	if err != nil {
		t.Fatalf("agent.New with handoff store: %v", err)
	}
	if a == nil {
		t.Fatal("agent is nil")
	}
}

func TestRoundTrip_PreservesEveryField(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	want := sampleRequest()

	if err := s.SaveHandoff(ctx, "conv-1", want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, found, err := s.LoadHandoff(ctx, "conv-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}

	if got.Reason != want.Reason {
		t.Errorf("Reason = %q, want %q", got.Reason, want.Reason)
	}
	if got.Question != want.Question {
		t.Errorf("Question = %q, want %q", got.Question, want.Question)
	}
	if got.ConversationID != want.ConversationID {
		t.Errorf("ConversationID = %q, want %q", got.ConversationID, want.ConversationID)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(got.Messages))
	}
}

// ContentBlock is a sealed interface, so it cannot round-trip through plain JSON.
// The store must use the type-discriminated conversation encoding — this asserts
// the concrete block type survives, not merely the message count.
func TestRoundTrip_ContentBlockTypesSurvive(t *testing.T) {
	s := newStore()
	ctx := context.Background()

	if err := s.SaveHandoff(ctx, "conv-1", sampleRequest()); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _, err := s.LoadHandoff(ctx, "conv-1")
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

// The interface documents nil, false, nil for a missing value rather than an
// error sentinel.
func TestLoad_MissingReturnsNotFoundWithoutError(t *testing.T) {
	s := newStore()
	got, found, err := s.LoadHandoff(context.Background(), "absent")
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if found {
		t.Error("found = true, want false")
	}
	if got != nil {
		t.Errorf("request = %+v, want nil", got)
	}
}

func TestDelete_RemovesHandoff(t *testing.T) {
	s := newStore()
	ctx := context.Background()

	if err := s.SaveHandoff(ctx, "conv-1", sampleRequest()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.DeleteHandoff(ctx, "conv-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, found, err := s.LoadHandoff(ctx, "conv-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if found {
		t.Error("found = true after delete, want false")
	}
}

func TestDelete_UnknownConversationIsNotError(t *testing.T) {
	s := newStore()
	if err := s.DeleteHandoff(context.Background(), "absent"); err != nil {
		t.Errorf("delete: %v", err)
	}
}

func TestSave_RejectsEmptyConversationID(t *testing.T) {
	s := newStore()
	if err := s.SaveHandoff(context.Background(), "", sampleRequest()); err == nil {
		t.Error("expected error for empty conversation ID, got nil")
	}
}

func TestSave_RejectsNilRequest(t *testing.T) {
	s := newStore()
	if err := s.SaveHandoff(context.Background(), "conv-1", nil); err == nil {
		t.Error("expected error for nil request, got nil")
	}
}

// Repeated saves append versions; Load must return the newest.
func TestSave_LatestWins(t *testing.T) {
	s := newStore()
	ctx := context.Background()

	for _, q := range []string{"first?", "second?", "third?"} {
		hr := sampleRequest()
		hr.Question = q
		if err := s.SaveHandoff(ctx, "conv-1", hr); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	got, _, err := s.LoadHandoff(ctx, "conv-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Question != "third?" {
		t.Errorf("Question = %q, want third?", got.Question)
	}
}

// Earlier saves stay reachable through the Checkpointer, which is the audit trail
// a plain key/value HandoffStore could not offer.
func TestEarlierVersionsRemainAvailableAsAuditTrail(t *testing.T) {
	cp := checkpoint.NewMemory()
	s := New(cp)
	ctx := context.Background()

	for _, q := range []string{"first?", "second?"} {
		hr := sampleRequest()
		hr.Question = q
		if err := s.SaveHandoff(ctx, "conv-1", hr); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	metas, err := cp.History(ctx, DefaultThreadPrefix+"conv-1")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(metas))
	}
	if metas[0].Label != checkpointLabel {
		t.Errorf("label = %q, want %q", metas[0].Label, checkpointLabel)
	}
}

func TestConversationsAreIsolated(t *testing.T) {
	s := newStore()
	ctx := context.Background()

	a := sampleRequest()
	a.Question = "for a?"
	b := sampleRequest()
	b.Question = "for b?"

	if err := s.SaveHandoff(ctx, "conv-a", a); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := s.SaveHandoff(ctx, "conv-b", b); err != nil {
		t.Fatalf("save b: %v", err)
	}

	got, _, err := s.LoadHandoff(ctx, "conv-a")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Question != "for a?" {
		t.Errorf("Question = %q, want %q", got.Question, "for a?")
	}

	if err := s.DeleteHandoff(ctx, "conv-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, _ := s.LoadHandoff(ctx, "conv-b"); !found {
		t.Error("deleting conv-a also removed conv-b")
	}
}

func TestThreadPrefix_NamespacesAndIsConfigurable(t *testing.T) {
	cp := checkpoint.NewMemory()
	s := New(cp, WithThreadPrefix("hs/"))
	ctx := context.Background()

	if err := s.SaveHandoff(ctx, "conv-1", sampleRequest()); err != nil {
		t.Fatalf("save: %v", err)
	}

	ids, err := cp.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 1 || ids[0] != "hs/conv-1" {
		t.Errorf("thread IDs = %v, want [hs/conv-1]", ids)
	}
}

// A handoff store must not collide with other consumers of a shared backing store.
func TestDoesNotCollideWithOtherCheckpointConsumers(t *testing.T) {
	cp := checkpoint.NewMemory()
	s := New(cp)
	ctx := context.Background()

	// An unrelated consumer using the bare conversation ID as its thread.
	if _, err := cp.Save(ctx, "conv-1", checkpoint.Checkpoint{
		State: checkpoint.State{"unrelated": true},
	}); err != nil {
		t.Fatalf("foreign save: %v", err)
	}

	if err := s.SaveHandoff(ctx, "conv-1", sampleRequest()); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The foreign thread is untouched and still has no handoff payload.
	foreign, err := cp.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("foreign load: %v", err)
	}
	if foreign.State["unrelated"] != true {
		t.Error("foreign checkpoint was overwritten")
	}

	// Deleting the handoff must not delete the foreign thread.
	if err := s.DeleteHandoff(ctx, "conv-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := cp.Load(ctx, "conv-1"); errors.Is(err, checkpoint.ErrNotFound) {
		t.Error("deleting the handoff also deleted the foreign thread")
	}
}
