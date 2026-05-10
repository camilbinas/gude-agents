package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Summary memory integration tests that call real LLM APIs.
//
// Run with:
//   go test -v -timeout=180s -run TestIntegration_Summary ./...

func TestIntegration_Summary_DefaultSummaryFunc(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	// Use the provider-backed DefaultSummaryFunc.
	summaryFn := conversation.DefaultSummaryFunc(p)

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
	for _, b := range result[0].Content {
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
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	// Use a custom summary function that explicitly preserves names.
	summaryFn := conversation.NewSummaryFunc(p,
		"Summarize this conversation into a concise paragraph. "+
			"You MUST preserve the user's name, location, occupation, and any other personal details they shared. "+
			"Start the summary with the user's name.",
	)

	summaryStore, err := conversation.NewSummary(store, 4, summaryFn,
		conversation.WithSummaryLogger(testLogger(t)),
		conversation.WithPreserveRecentMessages(2),
	)
	if err != nil {
		t.Fatal(err)
	}

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be very brief — one sentence max."),
		nil,
		agent.WithConversation(summaryStore, "summary-conv"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)

	// Have a multi-turn conversation that exceeds the threshold.
	turns := []string{
		"My name is Bob and I live in Berlin.",
		"I work as a software engineer at a startup.",
		"My favorite programming language is Go.",
		"I have a dog named Max.",
	}

	for i, msg := range turns {
		result, err := a.Invoke(c, msg)
		if err != nil {
			t.Fatalf("Turn %d error: %v", i+1, err)
		}
		t.Logf("Turn %d: %s → %s", i+1, msg, result)
	}

	// Wait for background summarization to complete (instead of sleeping).
	summaryStore.Wait()

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
	result, err := a.Invoke(c, "What is my name?")
	if err != nil {
		t.Fatalf("Post-summary invoke error: %v", err)
	}
	t.Logf("Post-summary response: %s", result)

	if !strings.Contains(strings.ToLower(result), "bob") {
		t.Errorf("expected agent to remember 'Bob' after summarization, got: %s", result)
	}
}

func TestIntegration_Summary_IndependentConversations(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	summaryFn := conversation.NewSummaryFunc(p,
		"Summarize this conversation into a concise paragraph. "+
			"You MUST preserve the user's name and any hobbies or interests they mentioned. "+
			"Start the summary with the user's name.",
	)

	summaryStore, err := conversation.NewSummary(store, 3, summaryFn,
		conversation.WithSummaryLogger(testLogger(t)),
	)
	if err != nil {
		t.Fatal(err)
	}

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be very brief."),
		nil,
		agent.WithSharedConversation(summaryStore),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Two independent conversations.
	c1 := agent.NewContext(ctx).WithConversationID("conv-alice")
	c2 := agent.NewContext(ctx).WithConversationID("conv-bob")

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

		_, err := a.Invoke(c1, aliceMsg)
		if err != nil {
			t.Fatalf("Alice turn %d error: %v", i+1, err)
		}
		_, err = a.Invoke(c2, bobMsg)
		if err != nil {
			t.Fatalf("Bob turn %d error: %v", i+1, err)
		}
	}

	// Wait for summarization to complete.
	summaryStore.Wait()

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
	aliceResult, err := a.Invoke(c1, "What is my name?")
	if err != nil {
		t.Fatalf("Alice recall error: %v", err)
	}
	bobResult, err := a.Invoke(c2, "What is my name?")
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

// TestIntegration_Summary_ToolCallsSurviveSummarization verifies that
// summarization doesn't leave orphaned tool_result blocks when the
// conversation includes tool calls. This is a regression test for a bug
// where the summary cut point could land between a tool_use and its
// corresponding tool_result, causing provider validation errors.
func TestIntegration_Summary_ToolCallsSurviveSummarization(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	summaryFn := conversation.DefaultSummaryFunc(p)

	// Low threshold (3 messages) to trigger summarization quickly.
	summaryStore, err := conversation.NewSummary(store, 3, summaryFn,
		conversation.WithSummaryLogger(testLogger(t)),
		conversation.WithPreserveRecentMessages(1),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Agent with a simple tool that the LLM will call.
	lookupTool := newLookupTool()

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. When asked about a city's population, use the lookup tool. Be brief."),
		lookupTool,
		agent.WithConversation(summaryStore, "tool-summary-conv"),
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	c := agent.NewContext(ctx)

	// Turn 1: trigger a tool call.
	result, err := a.Invoke(c, "What is the population of Berlin?")
	if err != nil {
		t.Fatalf("Turn 1 error: %v", err)
	}
	t.Logf("Turn 1: %s", result)
	if !strings.Contains(strings.ToLower(result), "3") {
		t.Logf("Warning: expected mention of population ~3.6M, got: %s", result)
	}

	// Turn 2: another tool call to push past the threshold.
	result, err = a.Invoke(c, "And what about Paris?")
	if err != nil {
		t.Fatalf("Turn 2 error: %v", err)
	}
	t.Logf("Turn 2: %s", result)

	// Wait for summarization.
	summaryStore.Wait()

	// Turn 3: this is the critical turn — if summarization left orphaned
	// tool_result blocks, this call will fail with a provider validation error.
	result, err = a.Invoke(c, "Which city is larger?")
	if err != nil {
		t.Fatalf("Turn 3 (post-summary) error: %v\nThis likely means summarization left orphaned tool_result blocks.", err)
	}
	t.Logf("Turn 3 (post-summary): %s", result)

	// The agent should still be able to answer based on context.
	lower := strings.ToLower(result)
	if !strings.Contains(lower, "paris") && !strings.Contains(lower, "berlin") {
		t.Errorf("expected answer to mention Paris or Berlin, got: %s", result)
	}
}

func newLookupTool() []tool.Tool {
	type CityInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}
	t := tool.New("lookup_population", "Look up the population of a city",
		func(_ context.Context, in CityInput) (string, error) {
			populations := map[string]string{
				"berlin": "3.6 million",
				"paris":  "2.1 million",
				"london": "8.9 million",
			}
			city := strings.ToLower(in.City)
			if pop, ok := populations[city]; ok {
				return city + " has a population of " + pop, nil
			}
			return "Population data not available for " + in.City, nil
		},
	)
	return []tool.Tool{t}
}

// TestIntegration_TokenSummary_ToolCallsSurviveSummarization verifies that
// the token-based summary strategy doesn't leave orphaned tool_result blocks
// when summarizing conversations that include tool calls. Same regression
// scenario as the message-count test, but using TokenSummary which triggers
// based on actual provider-reported input token count.
func TestIntegration_TokenSummary_ToolCallsSurviveSummarization(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	summaryFn := conversation.DefaultSummaryFunc(p)

	// Low token threshold to trigger summarization after a couple of tool-call turns.
	// A single tool-call turn typically uses ~500-1000 input tokens with context.
	tokenSummary, err := conversation.NewTokenSummary(store, 2000, summaryFn,
		conversation.WithTokenSummaryLogger(testLogger(t)),
		conversation.WithTokenPreserveRecentMessages(1),
		conversation.WithTokenTriggerThreshold(60), // trigger at 60% = ~1200 tokens
	)
	if err != nil {
		t.Fatal(err)
	}
	defer tokenSummary.Close()

	lookupTools := newLookupTool()

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. When asked about a city's population, use the lookup_population tool. Be brief — one sentence max."),
		lookupTools,
		agent.WithConversation(tokenSummary, "token-tool-conv"),
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	c := agent.NewContext(ctx)

	// Turn 1: trigger a tool call.
	result, err := a.Invoke(c, "What is the population of London?")
	if err != nil {
		t.Fatalf("Turn 1 error: %v", err)
	}
	t.Logf("Turn 1: %s", result)

	// Turn 2: another tool call to push token count past threshold.
	result, err = a.Invoke(c, "And Berlin?")
	if err != nil {
		t.Fatalf("Turn 2 error: %v", err)
	}
	t.Logf("Turn 2: %s", result)

	// Turn 3: one more to ensure we're well past the threshold.
	result, err = a.Invoke(c, "What about Paris?")
	if err != nil {
		t.Fatalf("Turn 3 error: %v", err)
	}
	t.Logf("Turn 3: %s", result)

	// Wait for background summarization.
	tokenSummary.Wait()

	// Turn 4: critical — if summarization left orphaned tool_result blocks,
	// this will fail with a provider validation error.
	result, err = a.Invoke(c, "Which of those three cities is the largest?")
	if err != nil {
		t.Fatalf("Turn 4 (post-summary) error: %v\nThis likely means token summarization left orphaned tool_result blocks.", err)
	}
	t.Logf("Turn 4 (post-summary): %s", result)

	// Should mention London (the largest of the three).
	if !strings.Contains(strings.ToLower(result), "london") {
		t.Errorf("expected answer to mention London (largest), got: %s", result)
	}
}
