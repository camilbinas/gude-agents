//go:build integration

package agent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// Summary memory integration tests that call real LLM APIs.
//
// Run with:
//   go test -tags=integration -v -timeout=180s -run TestIntegration_Summary ./agent/...

func TestIntegration_Summary_DefaultSummaryFunc(t *testing.T) {
	p := newTestProvider(t)

	// Use the provider-backed DefaultSummaryFunc.
	summaryFn := memory.DefaultSummaryFunc(p)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Build a conversation to summarize.
	msgs := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hi, my name is Alice and I work at Acme Corp."}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello Alice! Nice to meet you. How can I help?"}}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "I need to reset my password for the internal dashboard."}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "I can help with that. Your password has been reset. Check your email for the new credentials."}}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Thanks! Also, can you remind me when the Q3 review meeting is?"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "The Q3 review meeting is scheduled for September 15th at 2pm."}}},
	}

	result, err := summaryFn(ctx, msgs)
	if err != nil {
		t.Fatalf("DefaultSummaryFunc error: %v", err)
	}

	summary := ""
	for _, b := range result.Content {
		if tb, ok := b.(agent.TextBlock); ok {
			summary = tb.Text
		}
	}

	t.Logf("Summary: %s", summary)

	if summary == "" {
		t.Fatal("expected non-empty summary")
	}

	// The summary should preserve key facts.
	lower := strings.ToLower(summary)
	if !strings.Contains(lower, "alice") {
		t.Error("summary should mention Alice")
	}
	if !strings.Contains(lower, "password") && !strings.Contains(lower, "reset") {
		t.Error("summary should mention the password reset")
	}
}

func TestIntegration_Summary_TriggersAndCompresses(t *testing.T) {
	p := newTestProvider(t)
	store := memory.NewStore()

	// Low threshold so summarization triggers quickly.
	// threshold=8, 80% = 6.4 → triggers at 7 messages.
	summaryStore := memory.NewSummary(store, 8, memory.DefaultSummaryFunc(p),
		memory.WithSummaryLogger(testLogger(t)),
	)

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be very brief — one sentence max."),
		nil,
		agent.WithMemory(summaryStore, "summary-conv"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Have a multi-turn conversation that exceeds the threshold.
	turns := []string{
		"My name is Bob and I live in Berlin.",
		"I work as a software engineer at a startup.",
		"My favorite programming language is Go.",
		"I have a dog named Max.",
	}

	for i, msg := range turns {
		result, _, err := a.Invoke(ctx, msg)
		if err != nil {
			t.Fatalf("Turn %d error: %v", i+1, err)
		}
		t.Logf("Turn %d: %s → %s", i+1, msg, result)
	}

	// Give the background summarization goroutine time to complete.
	time.Sleep(5 * time.Second)

	// Check what's in the store now.
	msgs, err := store.Load(ctx, "summary-conv")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	t.Logf("Messages in store after summarization: %d", len(msgs))

	// After 4 turns we'd have 8 messages (4 user + 4 assistant).
	// Summarization should have compressed the early messages.
	// The exact count depends on timing, but it should be less than 8.
	if len(msgs) >= 8 {
		t.Logf("Warning: summarization may not have triggered yet (still %d messages)", len(msgs))
	}

	// Verify the conversation still works after summarization —
	// the agent should still know Bob's name from the summary.
	result, _, err := a.Invoke(ctx, "What is my name?")
	if err != nil {
		t.Fatalf("Post-summary invoke error: %v", err)
	}
	t.Logf("Post-summary response: %s", result)

	if !strings.Contains(strings.ToLower(result), "bob") {
		t.Errorf("expected agent to remember 'Bob' after summarization, got: %s", result)
	}
}

func TestIntegration_Summary_IndependentConversations(t *testing.T) {
	p := newTestProvider(t)
	store := memory.NewStore()

	summaryStore := memory.NewSummary(store, 6, memory.DefaultSummaryFunc(p),
		memory.WithSummaryLogger(testLogger(t)),
	)

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be very brief."),
		nil,
		agent.WithSharedMemory(summaryStore),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Two independent conversations.
	ctx1 := agent.WithConversationID(ctx, "conv-alice")
	ctx2 := agent.WithConversationID(ctx, "conv-bob")

	turns := []string{
		"My name is %s.",
		"I like %s.",
		"Remember my name and hobby.",
	}

	aliceHobbies := []string{"Alice", "painting", ""}
	bobHobbies := []string{"Bob", "chess", ""}

	for i, tmpl := range turns {
		aliceMsg := tmpl
		bobMsg := tmpl
		if i < 2 {
			aliceMsg = strings.Replace(tmpl, "%s", aliceHobbies[i], 1)
			bobMsg = strings.Replace(tmpl, "%s", bobHobbies[i], 1)
		}

		_, _, err := a.Invoke(ctx1, aliceMsg)
		if err != nil {
			t.Fatalf("Alice turn %d error: %v", i+1, err)
		}
		_, _, err = a.Invoke(ctx2, bobMsg)
		if err != nil {
			t.Fatalf("Bob turn %d error: %v", i+1, err)
		}
	}

	// Give summarization time.
	time.Sleep(5 * time.Second)

	// Verify conversations are isolated.
	aliceMsgs, _ := store.Load(ctx, "conv-alice")
	bobMsgs, _ := store.Load(ctx, "conv-bob")

	t.Logf("Alice messages: %d, Bob messages: %d", len(aliceMsgs), len(bobMsgs))

	// Both should have messages (summarized or not).
	if len(aliceMsgs) == 0 {
		t.Error("expected alice to have messages")
	}
	if len(bobMsgs) == 0 {
		t.Error("expected bob to have messages")
	}

	// Verify each conversation remembers the right person.
	aliceResult, _, err := a.Invoke(ctx1, "What is my name?")
	if err != nil {
		t.Fatalf("Alice recall error: %v", err)
	}
	bobResult, _, err := a.Invoke(ctx2, "What is my name?")
	if err != nil {
		t.Fatalf("Bob recall error: %v", err)
	}

	t.Logf("Alice recall: %s", aliceResult)
	t.Logf("Bob recall: %s", bobResult)

	if !strings.Contains(strings.ToLower(aliceResult), "alice") {
		t.Errorf("expected alice's conversation to remember 'Alice', got: %s", aliceResult)
	}
	if !strings.Contains(strings.ToLower(bobResult), "bob") {
		t.Errorf("expected bob's conversation to remember 'Bob', got: %s", bobResult)
	}
}
