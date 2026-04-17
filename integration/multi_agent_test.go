package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Multi-agent composition integration tests that call real LLM APIs.
//
// Run with:
//   go test -v -timeout=180s -run TestIntegration_MultiAgent ./...

func TestIntegration_MultiAgent_OrchestratorDelegatesToWorker(t *testing.T) {
	p := newTestProvider(t)

	// Worker: a specialist that "looks up" project data.
	type SearchInput struct {
		Query string `json:"query" description:"Search term" required:"true"`
	}
	searchTool := tool.New("search_projects", "Search for projects by name",
		func(_ context.Context, in SearchInput) (string, error) {
			return `[{"name":"Atlas","status":"active","deadline":"2026-06-01"}]`, nil
		},
	)

	worker, err := agent.Worker(p,
		prompt.Text("You are a project researcher. Use the search_projects tool to find project details. Be brief."),
		[]tool.Tool{searchTool},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Orchestrator: routes to the worker via AgentAsTool.
	orchestrator, err := agent.Orchestrator(p,
		prompt.Text(
			"You are a helpful assistant. You have one specialist:\n"+
				"- ask_researcher: project details, statuses, deadlines\n"+
				"Route the user's question to the specialist and synthesize the response. Be brief.",
		),
		[]tool.Tool{
			agent.AgentAsTool("ask_researcher",
				"Ask about project details, statuses, and deadlines.",
				worker),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, usage, err := orchestrator.Invoke(ctx, "What's the status of the Atlas project?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Response: %s", result)
	t.Logf("Tokens: %d in, %d out", usage.InputTokens, usage.OutputTokens)

	lower := strings.ToLower(result)
	if !strings.Contains(lower, "atlas") && !strings.Contains(lower, "active") {
		t.Errorf("expected response to mention Atlas or active, got: %s", result)
	}
}

func TestIntegration_MultiAgent_ParallelSpecialists(t *testing.T) {
	p := newTestProvider(t)

	// Two workers with different specialties.
	projectWorker, err := agent.Worker(p,
		prompt.Text("You are a project researcher. When asked, respond with: 'Project Atlas is active, deadline June 2026.' Be brief."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	financeWorker, err := agent.Worker(p,
		prompt.Text("You are a financial analyst. When asked, respond with: 'Atlas revenue is €42,000 in Q1 2026.' Be brief."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	orchestrator, err := agent.Orchestrator(p,
		prompt.Text(
			"You are a helpful assistant with two specialists:\n"+
				"- ask_projects: project details and statuses\n"+
				"- ask_finance: revenue and financial data\n"+
				"For questions that span both domains, call both specialists. Synthesize their responses. Be brief.",
		),
		[]tool.Tool{
			agent.AgentAsTool("ask_projects", "Ask about project details.", projectWorker),
			agent.AgentAsTool("ask_finance", "Ask about revenue and finances.", financeWorker),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, _, err := orchestrator.Invoke(ctx,
		"Give me the status and revenue for the Atlas project.",
	)
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Response: %s", result)

	lower := strings.ToLower(result)
	if !strings.Contains(lower, "atlas") {
		t.Errorf("expected response to mention Atlas, got: %s", result)
	}
}
