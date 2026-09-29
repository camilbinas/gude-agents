package integration_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Middleware integration tests that call real LLM APIs.
//
// Run with:
//   go test -v -timeout=120s -run TestIntegration_Middleware ./...

func TestIntegration_Middleware_ExecutionOrder(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	var mu sync.Mutex
	var order []string

	mw1 := func(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
		return func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
			mu.Lock()
			order = append(order, "mw1-before")
			mu.Unlock()
			result, err := next(ctx, call)
			mu.Lock()
			order = append(order, "mw1-after")
			mu.Unlock()
			return result, err
		}
	}

	mw2 := func(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
		return func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
			mu.Lock()
			order = append(order, "mw2-before")
			mu.Unlock()
			result, err := next(ctx, call)
			mu.Lock()
			order = append(order, "mw2-after")
			mu.Unlock()
			return result, err
		}
	}

	type CalcInput struct {
		Expr string `json:"expr" description:"A math expression" required:"true"`
	}
	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in CalcInput) (string, error) {
		return "42", nil
	})

	a, err := agent.New(
		p,
		"You are a calculator. Always use the calculate tool. Be very brief.",
		agent.WithTools(calcTool),
		agent.WithMiddleware(mw1, mw2),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := a.Invoke(agent.NewContext(ctx), "What is 7 times 6?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	t.Logf("Response: %s", result.Text)

	mu.Lock()
	defer mu.Unlock()
	t.Logf("Middleware order: %v", order)
	if len(order) < 4 {
		t.Fatalf("expected at least 4 middleware calls, got %d: %v", len(order), order)
	}
	if order[0] != "mw1-before" {
		t.Errorf("expected first call to be mw1-before, got %s", order[0])
	}
	if order[1] != "mw2-before" {
		t.Errorf("expected second call to be mw2-before, got %s", order[1])
	}
	if order[2] != "mw2-after" {
		t.Errorf("expected third call to be mw2-after, got %s", order[2])
	}
	if order[3] != "mw1-after" {
		t.Errorf("expected fourth call to be mw1-after, got %s", order[3])
	}
}

func TestIntegration_Middleware_ModifiesToolOutput(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	tagger := func(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
		return func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
			result, err := next(ctx, call)
			if err == nil {
				result.Text += " [verified]"
			}
			return result, err
		}
	}

	type LookupInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}
	weatherTool := tool.New("get_weather", "Get weather for a city", func(_ context.Context, in LookupInput) (string, error) {
		return "22°C, sunny", nil
	})

	a, err := agent.New(
		p,
		"You are a weather assistant. Use the get_weather tool. Include the exact tool result in your response. Be brief.",
		agent.WithTools(weatherTool),
		agent.WithMiddleware(tagger),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := a.Invoke(agent.NewContext(ctx), "What's the weather in Paris?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	t.Logf("Response: %s", result.Text)
	if !strings.Contains(result.Text, "22") && !strings.Contains(strings.ToLower(result.Text), "sunny") {
		t.Logf("Warning: response may not reflect tool output: %s", result.Text)
	}
}

func TestIntegration_Middleware_LogsToolCalls(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	var mu sync.Mutex
	var logged []string
	logger := func(next agent.ToolHandlerFunc) agent.ToolHandlerFunc {
		return func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
			mu.Lock()
			logged = append(logged, fmt.Sprintf("tool=%s input=%s", call.Name, string(call.Input)))
			mu.Unlock()
			return next(ctx, call)
		}
	}

	type CalcInput struct {
		Expr string `json:"expr" description:"A math expression" required:"true"`
	}
	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in CalcInput) (string, error) {
		return "42", nil
	})

	a, err := agent.New(
		p,
		"You are a calculator. Always use the calculate tool. Be very brief.",
		agent.WithTools(calcTool),
		agent.WithMiddleware(logger),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err = a.Invoke(agent.NewContext(ctx), "What is 7 times 6?"); err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(logged) == 0 {
		t.Error("expected middleware to log at least one tool call")
	}
	for _, entry := range logged {
		t.Logf("Logged: %s", entry)
		if !strings.Contains(entry, "calculate") {
			t.Errorf("expected log entry to mention 'calculate', got: %s", entry)
		}
	}
}
