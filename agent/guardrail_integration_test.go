//go:build integration

package agent_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// Guardrail integration tests that call real LLM APIs.
//
// Run with:
//   go test -tags=integration -v -timeout=120s -run TestIntegration_Guardrail ./agent/...

func TestIntegration_Guardrail_InputTransform(t *testing.T) {
	p := newTestProvider(t)

	// Input guardrail that prepends a prefix to every message.
	prefixGuardrail := func(_ context.Context, msg string) (string, error) {
		return "IMPORTANT CONTEXT: The user is a premium customer.\n\n" + msg, nil
	}

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. If the user is a premium customer, mention their premium status in your response. Be brief."),
		nil,
		agent.WithInputGuardrail(prefixGuardrail),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "Hello, what services do I have access to?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Response: %s", result)

	lower := strings.ToLower(result)
	if !strings.Contains(lower, "premium") {
		t.Errorf("expected response to mention premium status, got: %s", result)
	}
}

func TestIntegration_Guardrail_InputBlock(t *testing.T) {
	p := newTestProvider(t)

	// Input guardrail that blocks messages containing "password".
	blockGuardrail := func(_ context.Context, msg string) (string, error) {
		if strings.Contains(strings.ToLower(msg), "password") {
			return "", errors.New("messages containing sensitive information are not allowed")
		}
		return msg, nil
	}

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant."),
		nil,
		agent.WithInputGuardrail(blockGuardrail),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Should be blocked — never reaches the LLM.
	_, _, err = a.Invoke(ctx, "My password is hunter2")
	if err == nil {
		t.Fatal("expected guardrail error, got nil")
	}

	var ge *agent.GuardrailError
	if !errors.As(err, &ge) {
		t.Fatalf("expected *GuardrailError, got %T: %v", err, err)
	}
	if ge.Direction != "input" {
		t.Errorf("expected direction=input, got %s", ge.Direction)
	}
	t.Logf("Blocked as expected: %v", err)

	// Should pass — no sensitive content.
	result, _, err := a.Invoke(ctx, "What is the capital of France?")
	if err != nil {
		t.Fatalf("expected clean message to pass, got: %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "paris") {
		t.Logf("Response: %s", result)
	}
}

func TestIntegration_Guardrail_OutputTransform(t *testing.T) {
	p := newTestProvider(t)

	// Output guardrail that appends a disclaimer.
	disclaimerGuardrail := func(_ context.Context, response string) (string, error) {
		return response + "\n\n---\nDisclaimer: This is not financial advice.", nil
	}

	a, err := agent.New(p,
		prompt.Text("You are a financial assistant. Be brief."),
		nil,
		agent.WithOutputGuardrail(disclaimerGuardrail),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "Should I invest in index funds?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Response: %s", result)

	if !strings.Contains(result, "Disclaimer: This is not financial advice.") {
		t.Errorf("expected disclaimer appended to response, got: %s", result)
	}
}

func TestIntegration_Guardrail_OutputBlock(t *testing.T) {
	p := newTestProvider(t)

	// Output guardrail that blocks responses mentioning specific topics.
	topicBlocker := func(_ context.Context, response string) (string, error) {
		blocked := []string{"nuclear", "weapon", "explosive"}
		lower := strings.ToLower(response)
		for _, word := range blocked {
			if strings.Contains(lower, word) {
				return "", fmt.Errorf("response contains blocked topic: %s", word)
			}
		}
		return response, nil
	}

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be brief."),
		nil,
		agent.WithOutputGuardrail(topicBlocker),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Safe question — should pass.
	result, _, err := a.Invoke(ctx, "What is the capital of Japan?")
	if err != nil {
		t.Fatalf("safe question failed: %v", err)
	}
	t.Logf("Safe response: %s", result)

	if !strings.Contains(strings.ToLower(result), "tokyo") {
		t.Logf("Warning: expected Tokyo in response")
	}
}

func TestIntegration_Guardrail_ChainedGuardrails(t *testing.T) {
	p := newTestProvider(t)

	callOrder := make([]string, 0)

	// Two input guardrails run in order.
	g1 := func(_ context.Context, msg string) (string, error) {
		callOrder = append(callOrder, "input-1")
		return strings.ToUpper(msg), nil
	}
	g2 := func(_ context.Context, msg string) (string, error) {
		callOrder = append(callOrder, "input-2")
		return msg + " [verified]", nil
	}

	// Output guardrail.
	g3 := func(_ context.Context, resp string) (string, error) {
		callOrder = append(callOrder, "output-1")
		return resp + " [reviewed]", nil
	}

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be very brief — one sentence max."),
		nil,
		agent.WithInputGuardrail(g1, g2),
		agent.WithOutputGuardrail(g3),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "hello")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Response: %s", result)
	t.Logf("Call order: %v", callOrder)

	if len(callOrder) < 3 {
		t.Errorf("expected at least 3 guardrail calls, got %d: %v", len(callOrder), callOrder)
	}
	if len(callOrder) >= 2 && (callOrder[0] != "input-1" || callOrder[1] != "input-2") {
		t.Errorf("expected input guardrails to run in order, got: %v", callOrder)
	}
	if !strings.HasSuffix(result, "[reviewed]") {
		t.Errorf("expected output to end with [reviewed], got: %s", result)
	}
}
