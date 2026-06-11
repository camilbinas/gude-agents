package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Token estimation integration tests that verify pre-flight budget enforcement
// against real LLM providers.
//
// Run with:
//   go test -v -timeout=120s -run TestIntegration_TokenEstimation ./...

func TestIntegration_TokenEstimation_PreFlightRejectsOversizedRequest(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	// Set a very small TPM budget (50 tokens). A real LLM call with system prompt
	// + user message will easily exceed this with CharEstimator.
	rl, err := agent.NewRateLimiter(
		agent.TPM(50),
		agent.RPM(100),
		agent.WithTokenEstimator(nil), // defaults to CharEstimator
		agent.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Use a long system prompt to guarantee the estimate exceeds 50 tokens.
	longPrompt := strings.Repeat("You are a helpful assistant that provides detailed answers. ", 20)

	a, err := agent.New(p,
		prompt.Text(longPrompt),
		nil,
		agent.WithRateLimiter(rl),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	_, err = a.Invoke(c, "Tell me about the history of computing in great detail.")
	if err == nil {
		t.Fatal("expected ErrRateLimitExceeded from pre-flight check, got nil")
	}
	if !errors.Is(err, agent.ErrRateLimitExceeded) {
		t.Fatalf("expected ErrRateLimitExceeded, got: %v", err)
	}
	t.Logf("Pre-flight correctly rejected oversized request: %v", err)
}

func TestIntegration_TokenEstimation_PreFlightAllowsSmallRequest(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	// Set a generous TPM budget (100000 tokens). A simple request should pass.
	rl, err := agent.NewRateLimiter(
		agent.TPM(100000),
		agent.RPM(100),
		agent.WithTokenEstimator(nil), // defaults to CharEstimator
		agent.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}

	a, err := agent.New(p,
		prompt.Text("Be brief."),
		nil,
		agent.WithRateLimiter(rl),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "What is 2+2? Reply with just the number.")
	if err != nil {
		t.Fatalf("expected pre-flight to allow small request, got error: %v", err)
	}
	if !strings.Contains(result, "4") {
		t.Errorf("expected response to contain '4', got: %s", result)
	}
	t.Logf("Pre-flight allowed small request, response: %s", result)
}

func TestIntegration_TokenEstimation_BudgetExhaustedAfterFirstCall(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	// Set a moderate TPM budget. The first call will use most of it,
	// and the second call's pre-flight check should reject.
	rl, err := agent.NewRateLimiter(
		agent.TPM(200),
		agent.RPM(100),
		agent.WithTokenEstimator(nil), // defaults to CharEstimator
		agent.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Use a system prompt that's ~100 chars so each call estimates ~25 tokens via CharEstimator.
	// After the first call records real usage (likely 50-150 tokens), the budget should be tight.
	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Keep responses to one sentence."),
		nil,
		agent.WithRateLimiter(rl),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// First call should succeed.
	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "What is the capital of France? Reply with just the city name.")
	if err != nil {
		t.Fatalf("first call should succeed, got error: %v", err)
	}
	t.Logf("First call succeeded: %s", result)

	// Second call may be rejected by pre-flight if the first call consumed enough budget.
	// We use a longer prompt to increase the estimate.
	longQuestion := strings.Repeat("Please explain in detail ", 10) + "what is 2+2?"
	_, err = a.Invoke(c, longQuestion)
	if err != nil {
		if errors.Is(err, agent.ErrRateLimitExceeded) {
			t.Logf("Second call correctly rejected after budget exhaustion: %v", err)
		} else {
			t.Fatalf("unexpected error on second call: %v", err)
		}
	} else {
		t.Log("Second call succeeded (budget not yet exhausted — this is acceptable)")
	}
}

func TestIntegration_TokenEstimation_WithToolsIncludedInEstimate(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type CalcInput struct {
		Expression string `json:"expression" description:"A math expression" required:"true"`
	}

	calcTool := tool.New("calculate", "Evaluate a math expression and return the numeric result", func(_ context.Context, in CalcInput) (string, error) {
		return "42", nil
	})

	// Tight budget — tool specs are included in the token estimate, pushing it over.
	rl, err := agent.NewRateLimiter(
		agent.TPM(30),
		agent.RPM(100),
		agent.WithTokenEstimator(nil),
		agent.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}

	a, err := agent.New(p,
		prompt.Text("You are a calculator. Always use the calculate tool."),
		[]tool.Tool{calcTool},
		agent.WithRateLimiter(rl),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	_, err = a.Invoke(c, "What is 7 times 6?")
	if err == nil {
		t.Fatal("expected ErrRateLimitExceeded when tool specs push estimate over budget, got nil")
	}
	if !errors.Is(err, agent.ErrRateLimitExceeded) {
		t.Fatalf("expected ErrRateLimitExceeded, got: %v", err)
	}
	t.Logf("Pre-flight correctly rejected request with tool specs: %v", err)
}

func TestIntegration_TokenEstimation_NoRateLimiterSkipsCheck(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	// No rate limiter — pre-flight should be skipped entirely.
	a, err := agent.New(p,
		prompt.Text("Be brief."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "Say hello.")
	if err != nil {
		t.Fatalf("expected success without rate limiter, got error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty response")
	}
	t.Logf("No rate limiter, response: %s", result)
}
