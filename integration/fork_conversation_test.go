package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

func TestIntegration_ForkConversation_BranchesDivergeIndependently(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	a, err := agent.New(
		p,
		"You are a helpful assistant. Be concise. Always answer in one short sentence.",
		agent.WithConversationStore(store),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if _, err := a.Invoke(agent.NewContext(ctx).WithConversationID("main"), "My favorite color is blue. Just acknowledge."); err != nil {
		t.Fatalf("main turn 1: %v", err)
	}
	if err := agent.ForkConversation(ctx, store, "main", "branch"); err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}
	if _, err := a.Invoke(agent.NewContext(ctx).WithConversationID("main"), "I also like dogs. Just acknowledge."); err != nil {
		t.Fatalf("main turn 2: %v", err)
	}
	if _, err := a.Invoke(agent.NewContext(ctx).WithConversationID("branch"), "I also like sailing. Just acknowledge."); err != nil {
		t.Fatalf("branch turn 2: %v", err)
	}

	mainAnswer, err := a.Invoke(agent.NewContext(ctx).WithConversationID("main"), "List the personal preferences I shared with you, separated by commas.")
	if err != nil {
		t.Fatalf("main quiz: %v", err)
	}
	t.Logf("main quiz: %s", mainAnswer.Text)

	branchAnswer, err := a.Invoke(agent.NewContext(ctx).WithConversationID("branch"), "List the personal preferences I shared with you, separated by commas.")
	if err != nil {
		t.Fatalf("branch quiz: %v", err)
	}
	t.Logf("branch quiz: %s", branchAnswer.Text)

	mainLower := strings.ToLower(mainAnswer.Text)
	branchLower := strings.ToLower(branchAnswer.Text)
	if !strings.Contains(mainLower, "blue") {
		t.Errorf("main lost pre-fork fact: %s", mainAnswer.Text)
	}
	if !strings.Contains(branchLower, "blue") {
		t.Errorf("branch lost pre-fork fact: %s", branchAnswer.Text)
	}
	if !strings.Contains(mainLower, "dog") {
		t.Errorf("main forgot post-fork fact 'dogs': %s", mainAnswer.Text)
	}
	if strings.Contains(mainLower, "sail") {
		t.Errorf("main leaked branch fact 'sailing': %s", mainAnswer.Text)
	}
	if !strings.Contains(branchLower, "sail") {
		t.Errorf("branch forgot post-fork fact 'sailing': %s", branchAnswer.Text)
	}
	if strings.Contains(branchLower, "dog") {
		t.Errorf("branch leaked main fact 'dogs': %s", branchAnswer.Text)
	}
}

func TestIntegration_ForkConversation_FromEmpty(t *testing.T) {
	t.Parallel()
	store := conversation.NewInMemory()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := agent.ForkConversation(ctx, store, "never-existed", "fresh-branch"); err != nil {
		t.Fatalf("ForkConversation on missing source: %v", err)
	}

	snapshot, err := store.Load(ctx, "fresh-branch")
	if err != nil {
		t.Fatalf("Load fresh-branch: %v", err)
	}
	if len(snapshot.Messages) != 0 {
		t.Errorf("expected empty branch, got %d messages", len(snapshot.Messages))
	}
}
