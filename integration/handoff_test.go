package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Handoff integration tests that call real LLM APIs.
//
// Run with:
//   go test -v -timeout=180s -run TestIntegration_Handoff ./...

func TestIntegration_Handoff_PauseAndResume(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type LookupInput struct {
		OrderID string `json:"order_id" description:"The order ID" required:"true"`
	}
	lookupTool := tool.New("lookup_order", "Look up order details by ID",
		func(_ context.Context, in LookupInput) (string, error) {
			return `{"order_id":"` + in.OrderID + `","amount":"$89.99","item":"Headphones","status":"delivered"}`, nil
		},
	)

	a, err := agent.New(
		p,
		"You are a customer support agent. When a user asks for a refund: "+
			"1) Look up the order using lookup_order. "+
			"2) Then use request_human_input to ask a manager for approval before proceeding. "+
			"3) After receiving approval, confirm the refund to the user. Be brief.",
		agent.WithTools(
			lookupTool,
			agent.NewHumanInputTool("request_human_input", "Use when you need manager approval before processing a refund."),
		),
		agent.WithMaxIterations(10),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := agent.NewContext(ctx)

	paused, err := a.Invoke(c, "I need a refund for order #5678")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	in := requireHumanInputInterrupt(t, paused)

	t.Logf("Interrupt reason: %s", in.Input.Reason)
	t.Logf("Interrupt question: %s", in.Input.Question)
	t.Logf("Messages preserved: %d", len(in.Messages))
	if in.Input.Reason == "" {
		t.Error("expected non-empty interrupt reason")
	}
	if in.Input.Question == "" {
		t.Error("expected non-empty interrupt question")
	}
	if len(in.Messages) < 2 {
		t.Errorf("expected at least 2 messages in interrupt context, got %d", len(in.Messages))
	}

	result, err := a.Resume(c, in, agent.Respond("Approved. Process the refund."))
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}
	t.Logf("Resumed response: %s", result.Text)

	lower := strings.ToLower(result.Text)
	if !strings.Contains(lower, "refund") && !strings.Contains(lower, "approved") && !strings.Contains(lower, "processed") {
		t.Errorf("expected response to mention refund/approved/processed, got: %s", result.Text)
	}
}

func TestIntegration_Handoff_WithMemoryPersistence(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	a, err := agent.New(
		p,
		"You are a support agent. When the user asks to delete their account, use request_human_input to get confirmation from a supervisor. Be brief.",
		agent.WithTools(agent.NewHumanInputTool("request_human_input", "Use when the user asks to delete their account to get supervisor confirmation.")),
		agent.WithConversationStore(store),
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	convID := "handoff-conv-42"
	c := agent.NewContext(ctx).WithConversationID(convID)
	paused, err := a.Invoke(c, "I want to delete my account")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	in := requireHumanInputInterrupt(t, paused)
	if in.ConversationID != convID {
		t.Errorf("interrupt conversationID = %q, want %q", in.ConversationID, convID)
	}

	saved, err := store.Load(ctx, convID)
	if err != nil {
		t.Fatalf("load paused conversation: %v", err)
	}
	if len(saved.Messages) == 0 {
		t.Error("expected messages saved to memory on interrupt")
	}
	t.Logf("Messages saved on interrupt: %d", len(saved.Messages))

	resumeCtx := agent.NewContext(context.Background()).WithConversationID(convID)
	result, err := a.Resume(resumeCtx, in, agent.Respond("Supervisor approved the deletion."))
	if err != nil {
		t.Fatalf("Resume error: %v", err)
	}
	t.Logf("Resumed response: %s", result.Text)

	final, err := store.Load(context.Background(), convID)
	if err != nil {
		t.Fatalf("load resumed conversation: %v", err)
	}
	t.Logf("Final messages in memory: %d", len(final.Messages))
	if len(final.Messages) <= len(saved.Messages) {
		t.Errorf("expected more messages after resume, got %d (was %d on interrupt)", len(final.Messages), len(saved.Messages))
	}
}

func TestIntegration_Handoff_AgentDecidesToHandoff(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

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

	a, err := agent.New(
		p,
		"You are a support agent with access to an FAQ database. For simple questions, search the FAQ. "+
			"For complex issues like account deletion, billing disputes, or complaints, use request_human_input to escalate to a human agent. Be brief.",
		agent.WithTools(
			faqTool,
			agent.NewHumanInputTool("request_human_input", "Use for complex issues like account deletion, billing disputes, or complaints to escalate to a human agent."),
		),
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := a.Invoke(agent.NewContext(ctx), "What are your business hours?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result.StopReason == agent.StopInterrupt {
		t.Error("did not expect interrupt for a simple FAQ question")
	}
	t.Logf("FAQ response: %s", result.Text)
	if !strings.Contains(strings.ToLower(result.Text), "9") && !strings.Contains(strings.ToLower(result.Text), "monday") {
		t.Logf("Warning: response may not contain business hours: %s", result.Text)
	}

	paused, err := a.Invoke(agent.NewContext(ctx), "I want to file a formal complaint about being overcharged $500")
	if err != nil {
		t.Fatalf("Invoke complaint error: %v", err)
	}
	in := requireHumanInputInterrupt(t, paused)
	t.Logf("Interrupt triggered — reason: %s, question: %s", in.Input.Reason, in.Input.Question)
}

func requireHumanInputInterrupt(t *testing.T, result agent.Result) *agent.Interrupt {
	t.Helper()
	if result.StopReason != agent.StopInterrupt {
		t.Fatalf("stop reason = %q, want %q", result.StopReason, agent.StopInterrupt)
	}
	if result.Interrupt == nil {
		t.Fatal("expected interrupt result")
	}
	if result.Interrupt.Type != agent.InterruptHumanInput {
		t.Fatalf("interrupt type = %q, want %q", result.Interrupt.Type, agent.InterruptHumanInput)
	}
	if result.Interrupt.Input == nil {
		t.Fatal("human-input interrupt has no input payload")
	}
	return result.Interrupt
}
