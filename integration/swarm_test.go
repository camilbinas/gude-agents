package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/swarm"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Swarm integration tests that call real LLM APIs.
//
// Run with:
//   go test -v -timeout=180s -run TestIntegration_Swarm ./...

func TestIntegration_Swarm_SingleHandoff(t *testing.T) {
	p := newTestProvider(t)

	triage, err := agent.SwarmAgent(p, prompt.Text(
		"You are a triage agent. You CANNOT answer billing questions yourself. "+
			"If the user asks about refunds, invoices, or payments, you MUST transfer to billing immediately. "+
			"Do not attempt to answer — just transfer.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	type RefundInput struct {
		OrderID string `json:"order_id" description:"The order ID to refund" required:"true"`
	}
	refundTool := tool.New("process_refund", "Process a refund for an order",
		func(_ context.Context, in RefundInput) (string, error) {
			return `{"status":"refunded","order":"` + in.OrderID + `","amount":"$49.99"}`, nil
		},
	)

	billing, err := agent.SwarmAgent(p, prompt.Text(
		"You are a billing specialist. Help users with refunds and payments. "+
			"Use the process_refund tool when asked. Be brief.",
	), []tool.Tool{refundTool})
	if err != nil {
		t.Fatal(err)
	}

	sw, err := swarm.New([]swarm.Member{
		{Name: "triage", Description: "Routes requests to the right specialist", Agent: triage},
		{Name: "billing", Description: "Handles refunds, invoices, and payments", Agent: billing},
	}, swarm.WithMaxHandoffs(3))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := sw.Invoke(ctx, "I need a refund for order #1234")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Final agent: %s", result.FinalAgent)
	t.Logf("Handoffs: %v", result.HandoffHistory)
	t.Logf("Response: %s", result.Response)

	if result.FinalAgent != "billing" {
		t.Errorf("expected final agent to be billing, got %s", result.FinalAgent)
	}
	if len(result.HandoffHistory) == 0 {
		t.Error("expected at least one handoff")
	}
	if result.HandoffHistory[0].From != "triage" || result.HandoffHistory[0].To != "billing" {
		t.Errorf("expected triage→billing handoff, got %v", result.HandoffHistory[0])
	}
}

func TestIntegration_Swarm_MemoryAcrossTurns(t *testing.T) {
	p := newTestProvider(t)

	greeter, err := agent.SwarmAgent(p, prompt.Text(
		"You are a greeter. Say hello and remember the user's name. "+
			"If the user asks a technical question, transfer to technical. Be brief.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	technical, err := agent.SwarmAgent(p, prompt.Text(
		"You are a technical support agent. Answer technical questions briefly. "+
			"If the user asks a non-technical question, transfer to greeter.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	store := memory.NewStore()
	sw, err := swarm.New([]swarm.Member{
		{Name: "greeter", Description: "Greets users and handles general questions", Agent: greeter},
		{Name: "technical", Description: "Handles technical support questions", Agent: technical},
	},
		swarm.WithMemory(store, "swarm-conv-1"),
		
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Turn 1: greet
	r1, err := sw.Invoke(ctx, "Hi, my name is Alice.")
	if err != nil {
		t.Fatalf("Turn 1 error: %v", err)
	}
	t.Logf("Turn 1 [%s]: %s", r1.FinalAgent, r1.Response)

	// Turn 2: technical question — should hand off to technical
	r2, err := sw.Invoke(ctx, "How do I fix a segfault in C?")
	if err != nil {
		t.Fatalf("Turn 2 error: %v", err)
	}
	t.Logf("Turn 2 [%s]: %s", r2.FinalAgent, r2.Response)

	// Turn 3: follow-up — should still be with technical (remembered via memory)
	r3, err := sw.Invoke(ctx, "What about memory leaks?")
	if err != nil {
		t.Fatalf("Turn 3 error: %v", err)
	}
	t.Logf("Turn 3 [%s]: %s", r3.FinalAgent, r3.Response)

	// Verify memory persisted the conversation.
	msgs, _ := store.Load(ctx, "swarm-conv-1")
	if len(msgs) < 6 { // at least 3 user + 3 assistant messages
		t.Errorf("expected at least 6 messages in memory, got %d", len(msgs))
	}
}

func TestIntegration_Swarm_WithPerRequestConversationID(t *testing.T) {
	p := newTestProvider(t)

	alpha, err := agent.SwarmAgent(p, prompt.Text(
		"You are agent Alpha. Be brief. Remember what the user tells you.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	beta, err := agent.SwarmAgent(p, prompt.Text(
		"You are agent Beta. Be brief.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	store := memory.NewStore()
	sw, err := swarm.New([]swarm.Member{
		{Name: "alpha", Description: "General assistant", Agent: alpha},
		{Name: "beta", Description: "Secondary assistant", Agent: beta},
	},
		swarm.WithMemory(store, ""),
		
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Two separate conversations on the same swarm.
	ctx1 := agent.WithConversationID(ctx, "user-alice")
	ctx2 := agent.WithConversationID(ctx, "user-bob")

	r1, err := sw.Invoke(ctx1, "My name is Alice.")
	if err != nil {
		t.Fatalf("Alice turn 1 error: %v", err)
	}
	t.Logf("Alice [%s]: %s", r1.FinalAgent, r1.Response)

	r2, err := sw.Invoke(ctx2, "My name is Bob.")
	if err != nil {
		t.Fatalf("Bob turn 1 error: %v", err)
	}
	t.Logf("Bob [%s]: %s", r2.FinalAgent, r2.Response)

	// Verify conversations are isolated.
	aliceMsgs, _ := store.Load(ctx, "user-alice")
	bobMsgs, _ := store.Load(ctx, "user-bob")

	if len(aliceMsgs) < 2 {
		t.Errorf("expected at least 2 messages for alice, got %d", len(aliceMsgs))
	}
	if len(bobMsgs) < 2 {
		t.Errorf("expected at least 2 messages for bob, got %d", len(bobMsgs))
	}

	// Verify no cross-contamination: alice's messages shouldn't mention bob.
	for _, m := range aliceMsgs {
		for _, b := range m.Content {
			if tb, ok := b.(agent.TextBlock); ok {
				if strings.Contains(strings.ToLower(tb.Text), "bob") {
					t.Errorf("alice's conversation mentions bob: %s", tb.Text)
				}
			}
		}
	}
}
