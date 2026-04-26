package agent

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

func TestWithConversationID_OverridesDefault(t *testing.T) {
	sp := newScriptedProvider(
		&ProviderResponse{Text: "reply for conv-A"},
		&ProviderResponse{Text: "reply for conv-B"},
	)

	store := newTestMemoryStore()
	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(store, "default-conv"))
	if err != nil {
		t.Fatal(err)
	}

	// Invoke with per-request conversation ID "conv-A".
	ctxA := WithConversationID(context.Background(), "conv-A")
	result, _, err := a.Invoke(ctxA, "hello A")
	if err != nil {
		t.Fatal(err)
	}
	if result != "reply for conv-A" {
		t.Errorf("expected %q, got %q", "reply for conv-A", result)
	}

	// Invoke with per-request conversation ID "conv-B".
	ctxB := WithConversationID(context.Background(), "conv-B")
	result, _, err = a.Invoke(ctxB, "hello B")
	if err != nil {
		t.Fatal(err)
	}
	if result != "reply for conv-B" {
		t.Errorf("expected %q, got %q", "reply for conv-B", result)
	}

	// Verify each conversation was saved separately.
	msgsA, _ := store.Load(context.Background(), "conv-A")
	msgsB, _ := store.Load(context.Background(), "conv-B")
	msgsDefault, _ := store.Load(context.Background(), "default-conv")

	if len(msgsA) != 2 {
		t.Errorf("conv-A: expected 2 messages, got %d", len(msgsA))
	}
	if len(msgsB) != 2 {
		t.Errorf("conv-B: expected 2 messages, got %d", len(msgsB))
	}
	if len(msgsDefault) != 0 {
		t.Errorf("default-conv: expected 0 messages, got %d", len(msgsDefault))
	}
}

func TestWithConversationID_FallsBackToDefault(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "reply"})

	store := newTestMemoryStore()
	a, err := New(sp, prompt.Text("sys"), nil, WithConversation(store, "fallback"))
	if err != nil {
		t.Fatal(err)
	}

	// Invoke without per-request override — should use "fallback".
	_, _, err = a.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	msgs, _ := store.Load(context.Background(), "fallback")
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages in fallback conv, got %d", len(msgs))
	}
}

func TestWithSharedConversation_RequiresContextConversationID(t *testing.T) {
	sp := newScriptedProvider(
		&ProviderResponse{Text: "user-1 reply"},
		&ProviderResponse{Text: "user-2 reply"},
	)

	store := newTestMemoryStore()
	a, err := New(sp, prompt.Text("sys"), nil, WithSharedConversation(store))
	if err != nil {
		t.Fatal(err)
	}

	// Two different users, same agent instance.
	ctx1 := WithConversationID(context.Background(), "user-1")
	ctx2 := WithConversationID(context.Background(), "user-2")

	r1, _, err := a.Invoke(ctx1, "hi from user 1")
	if err != nil {
		t.Fatal(err)
	}
	r2, _, err := a.Invoke(ctx2, "hi from user 2")
	if err != nil {
		t.Fatal(err)
	}

	if r1 != "user-1 reply" {
		t.Errorf("user-1: expected %q, got %q", "user-1 reply", r1)
	}
	if r2 != "user-2 reply" {
		t.Errorf("user-2: expected %q, got %q", "user-2 reply", r2)
	}

	msgs1, _ := store.Load(context.Background(), "user-1")
	msgs2, _ := store.Load(context.Background(), "user-2")

	if len(msgs1) != 2 {
		t.Errorf("user-1: expected 2 messages, got %d", len(msgs1))
	}
	if len(msgs2) != 2 {
		t.Errorf("user-2: expected 2 messages, got %d", len(msgs2))
	}
}

func TestResolveConversationID_EmptyStringIgnored(t *testing.T) {
	ctx := WithConversationID(context.Background(), "")
	got := ResolveConversationID(ctx, "default")
	if got != "default" {
		t.Errorf("expected %q, got %q", "default", got)
	}
}
