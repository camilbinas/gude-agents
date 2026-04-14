//go:build integration

package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Handoff integration tests that call real LLM APIs.
//
// Run with:
//   go test -tags=integration -v -timeout=180s -run TestIntegration_Handoff ./agent/...

func TestIntegration_Handoff_PauseAndResume(t *testing.T) {
	p := newTestProvider(t)

	type LookupInput struct {
		OrderID string `json:"order_id" description:"The order ID" required:"true"`
	}
	lookupTool := tool.New("lookup_order", "Look up order details by ID",
		func(_ context.Context, in LookupInput) (string, error) {
			return `{"order_id":"` + in.OrderID + `","amount":"$89.99","item":"Headphones","status":"delivered"}`, nil
		},
	)

	a, err := agent.New(p, prompt.Text(
		"You are a customer support agent. When a user asks for a refund: "+
			"1) Look up the order using lookup_order. "+
			"2) Then use request_human_input to ask a manager for approval before proceeding. "+
			"3) After receiving approval, confirm the refund to the user. "+
			"Be brief.",
	), []tool.Tool{lookupTool, agent.HandoffTool()},
		agent.WithMaxIterations(10),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Step 1: Agent should look up the order, then request human approval.
	ic := agent.NewInvocationContext()
	ctx = agent.WithInvocationContext(ctx, ic)

	_, err = a.InvokeStream(ctx, "I need a refund for order #5678", nil)
	if !errors.Is(err, agent.ErrHandoffRequested) {
		t.Fatalf("expected ErrHandoffRequested, got: %v", err)
	}

	hr, ok := agent.GetHandoffRequest(ic)
	if !ok {
		t.Fatal("expected HandoffRequest in InvocationContext")
	}

	t.Logf("Handoff reason: %s", hr.Reason)
	t.Logf("Handoff question: %s", hr.Question)
	t.Logf("Messages preserved: %d", len(hr.Messages))

	if hr.Reason == "" {
		t.Error("expected non-empty handoff reason")
	}
	if hr.Question == "" {
		t.Error("expected non-empty handoff question")
	}
	if len(hr.Messages) < 2 {
		t.Errorf("expected at least 2 messages in handoff context, got %d", len(hr.Messages))
	}

	// Step 2: Resume with manager approval.
	result, _, err := a.ResumeInvoke(ctx, hr, "Approved. Process the refund.")
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}

	t.Logf("Resumed response: %s", result)

	lower := strings.ToLower(result)
	if !strings.Contains(lower, "refund") && !strings.Contains(lower, "approved") && !strings.Contains(lower, "processed") {
		t.Errorf("expected response to mention refund/approved/processed, got: %s", result)
	}
}

func TestIntegration_Handoff_WithMemoryPersistence(t *testing.T) {
	p := newTestProvider(t)
	store := memory.NewStore()

	a, err := agent.New(p, prompt.Text(
		"You are a support agent. When the user asks to delete their account, "+
			"use request_human_input to get confirmation from a supervisor. Be brief.",
	), []tool.Tool{agent.HandoffTool()},
		agent.WithSharedMemory(store),
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	convID := "handoff-conv-42"
	ic := agent.NewInvocationContext()
	ctx = agent.WithConversationID(ctx, convID)
	ctx = agent.WithInvocationContext(ctx, ic)

	_, err = a.InvokeStream(ctx, "I want to delete my account", nil)
	if !errors.Is(err, agent.ErrHandoffRequested) {
		t.Fatalf("expected ErrHandoffRequested, got: %v", err)
	}

	hr, _ := agent.GetHandoffRequest(ic)

	// Verify the handoff captured the correct conversation ID.
	if hr.ConversationID != convID {
		t.Errorf("handoff conversationID = %q, want %q", hr.ConversationID, convID)
	}

	// Verify messages were saved to memory.
	saved, _ := store.Load(ctx, convID)
	if len(saved) == 0 {
		t.Error("expected messages saved to memory on handoff")
	}
	t.Logf("Messages saved on handoff: %d", len(saved))

	// Resume — should save the completed conversation to the same key.
	result, _, err := a.ResumeInvoke(context.Background(), hr, "Supervisor approved the deletion.")
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}
	t.Logf("Resumed response: %s", result)

	// Verify the full conversation is in memory.
	final, _ := store.Load(context.Background(), convID)
	t.Logf("Final messages in memory: %d", len(final))
	if len(final) <= len(saved) {
		t.Errorf("expected more messages after resume, got %d (was %d on handoff)", len(final), len(saved))
	}
}

func TestIntegration_Handoff_AgentDecidesToHandoff(t *testing.T) {
	p := newTestProvider(t)

	// Give the agent a tool it CAN use, plus the handoff tool.
	// The agent should use the regular tool for normal questions
	// and only handoff when it genuinely needs human input.
	type FaqInput struct {
		Question string `json:"question" description:"The FAQ question" required:"true"`
	}
	faqTool := tool.New("search_faq", "Search the FAQ database",
		func(_ context.Context, in FaqInput) (string, error) {
			lower := strings.ToLower(in.Question)
			if strings.Contains(lower, "hours") || strings.Contains(lower, "open") {
				return "We are open Monday-Friday 9am-5pm.", nil
			}
			return "No FAQ entry found for this question.", nil
		},
	)

	a, err := agent.New(p, prompt.Text(
		"You are a support agent with access to an FAQ database. "+
			"For simple questions, search the FAQ. "+
			"For complex issues like account deletion, billing disputes, or complaints, "+
			"use request_human_input to escalate to a human agent. Be brief.",
	), []tool.Tool{faqTool, agent.HandoffTool()},
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Normal question — should NOT trigger handoff.
	result, _, err := a.Invoke(ctx, "What are your business hours?")
	if errors.Is(err, agent.ErrHandoffRequested) {
		t.Error("did not expect handoff for a simple FAQ question")
	}
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	t.Logf("FAQ response: %s", result)

	if !strings.Contains(strings.ToLower(result), "9") && !strings.Contains(strings.ToLower(result), "monday") {
		t.Logf("Warning: response may not contain business hours: %s", result)
	}

	// Complex question — SHOULD trigger handoff.
	ic := agent.NewInvocationContext()
	ctx2 := agent.WithInvocationContext(ctx, ic)

	_, err = a.InvokeStream(ctx2, "I want to file a formal complaint about being overcharged $500", nil)
	if !errors.Is(err, agent.ErrHandoffRequested) {
		t.Errorf("expected handoff for a complaint, got: %v", err)
	} else {
		hr, _ := agent.GetHandoffRequest(ic)
		t.Logf("Handoff triggered — reason: %s, question: %s", hr.Reason, hr.Question)
	}
}
