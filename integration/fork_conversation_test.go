package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// TestIntegration_ForkConversation_BranchesDivergeIndependently verifies
// that ForkConversation creates an independent branch of an existing
// conversation and that subsequent turns on each branch do not bleed
// into the other.
//
// This covers commit 1d9ac49 — ForkConversation function.
func TestIntegration_ForkConversation_BranchesDivergeIndependently(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be concise. Always answer in one short sentence."),
		nil,
		agent.WithSharedConversation(store),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Turn 1 on conversation "main": establish a fact.
	c := agent.NewContext(ctx).WithConversationID("main")
	if _, err := a.Invoke(c, "My favorite color is blue. Just acknowledge."); err != nil {
		t.Fatalf("main turn 1: %v", err)
	}

	// Fork "main" → "branch" before continuing.
	if err := agent.ForkConversation(ctx, store, "main", "branch"); err != nil {
		t.Fatalf("ForkConversation: %v", err)
	}

	// Continue main with a different fact.
	mainCtx := agent.NewContext(ctx).WithConversationID("main")
	if _, err := a.Invoke(mainCtx, "I also like dogs. Just acknowledge."); err != nil {
		t.Fatalf("main turn 2: %v", err)
	}

	// Continue branch with a different fact (NOT cats — to avoid LLM bias toward symmetry).
	branchCtx := agent.NewContext(ctx).WithConversationID("branch")
	if _, err := a.Invoke(branchCtx, "I also like sailing. Just acknowledge."); err != nil {
		t.Fatalf("branch turn 2: %v", err)
	}

	// Quiz main: should know about blue and dogs, NOT sailing.
	mainAnswer, err := a.Invoke(
		agent.NewContext(ctx).WithConversationID("main"),
		"List the personal preferences I shared with you, separated by commas.",
	)
	if err != nil {
		t.Fatalf("main quiz: %v", err)
	}
	t.Logf("main quiz: %s", mainAnswer)

	// Quiz branch: should know about blue and sailing, NOT dogs.
	branchAnswer, err := a.Invoke(
		agent.NewContext(ctx).WithConversationID("branch"),
		"List the personal preferences I shared with you, separated by commas.",
	)
	if err != nil {
		t.Fatalf("branch quiz: %v", err)
	}
	t.Logf("branch quiz: %s", branchAnswer)

	mainLower := strings.ToLower(mainAnswer)
	branchLower := strings.ToLower(branchAnswer)

	// Both should remember the pre-fork fact (blue).
	if !strings.Contains(mainLower, "blue") {
		t.Errorf("main lost pre-fork fact: %s", mainAnswer)
	}
	if !strings.Contains(branchLower, "blue") {
		t.Errorf("branch lost pre-fork fact: %s", branchAnswer)
	}

	// Main should know dogs but not sailing.
	if !strings.Contains(mainLower, "dog") {
		t.Errorf("main forgot post-fork fact 'dogs': %s", mainAnswer)
	}
	if strings.Contains(mainLower, "sail") {
		t.Errorf("main leaked branch fact 'sailing': %s", mainAnswer)
	}

	// Branch should know sailing but not dogs.
	if !strings.Contains(branchLower, "sail") {
		t.Errorf("branch forgot post-fork fact 'sailing': %s", branchAnswer)
	}
	if strings.Contains(branchLower, "dog") {
		t.Errorf("branch leaked main fact 'dogs': %s", branchAnswer)
	}
}

// TestIntegration_ForkConversation_FromEmpty verifies that forking a
// conversation that was never written produces an empty branch with no
// errors.
func TestIntegration_ForkConversation_FromEmpty(t *testing.T) {
	t.Parallel()
	store := conversation.NewInMemory()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := agent.ForkConversation(ctx, store, "never-existed", "fresh-branch"); err != nil {
		t.Fatalf("ForkConversation on missing source: %v", err)
	}

	msgs, err := store.Load(ctx, "fresh-branch")
	if err != nil {
		t.Fatalf("Load fresh-branch: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty branch, got %d messages", len(msgs))
	}
}
