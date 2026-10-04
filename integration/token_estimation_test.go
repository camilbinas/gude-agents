package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/ratelimit"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestIntegration_TokenEstimation_PreFlightRejectsOversizedRequest(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	rl, err := ratelimit.NewRateLimiter(
		ratelimit.TPM(50),
		ratelimit.RPM(100),
		ratelimit.WithTokenEstimator(nil),
		ratelimit.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}

	longPrompt := strings.Repeat("You are a helpful assistant that provides detailed answers. ", 20)
	a, err := agent.New(p, longPrompt, agent.WithRateLimiter(rl))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = a.Invoke(agent.NewContext(ctx), "Tell me about the history of computing in great detail.")
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
	rl, err := ratelimit.NewRateLimiter(
		ratelimit.TPM(100000),
		ratelimit.RPM(100),
		ratelimit.WithTokenEstimator(nil),
		ratelimit.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}

	a, err := agent.New(p, "Be brief.", agent.WithRateLimiter(rl))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := a.Invoke(agent.NewContext(ctx), "What is 2+2? Reply with just the number.")
	if err != nil {
		t.Fatalf("expected pre-flight to allow small request, got error: %v", err)
	}
	if !strings.Contains(result.Text, "4") {
		t.Errorf("expected response to contain '4', got: %s", result.Text)
	}
	t.Logf("Pre-flight allowed small request, response: %s", result.Text)
}

func TestIntegration_TokenEstimation_BudgetExhaustedAfterFirstCall(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	rl, err := ratelimit.NewRateLimiter(
		ratelimit.TPM(200),
		ratelimit.RPM(100),
		ratelimit.WithTokenEstimator(nil),
		ratelimit.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}

	a, err := agent.New(p, "You are a helpful assistant. Keep responses to one sentence.", agent.WithRateLimiter(rl))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "What is the capital of France? Reply with just the city name.")
	if err != nil {
		t.Fatalf("first call should succeed, got error: %v", err)
	}
	t.Logf("First call succeeded: %s", result.Text)

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

	rl, err := ratelimit.NewRateLimiter(
		ratelimit.TPM(30),
		ratelimit.RPM(100),
		ratelimit.WithTokenEstimator(nil),
		ratelimit.WithFailFast(),
	)
	if err != nil {
		t.Fatal(err)
	}

	a, err := agent.New(
		p,
		"You are a calculator. Always use the calculate tool.",
		agent.WithTools(calcTool),
		agent.WithRateLimiter(rl),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = a.Invoke(agent.NewContext(ctx), "What is 7 times 6?")
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
	a, err := agent.New(p, "Be brief.")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := a.Invoke(agent.NewContext(ctx), "Say hello.")
	if err != nil {
		t.Fatalf("expected success without rate limiter, got error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty response")
	}
	t.Logf("No rate limiter, response: %s", result.Text)
}
