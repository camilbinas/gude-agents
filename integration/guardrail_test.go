package integration_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

func TestIntegration_Guardrail_InputTransform(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	prefixGuardrail := func(_ *agent.Context, msg string) (string, error) {
		return "IMPORTANT CONTEXT: The user is a premium customer.\n\n" + msg, nil
	}

	a, err := agent.New(
		p,
		"You are a helpful assistant. If the user is a premium customer, mention their premium status in your response. Be brief.",
		agent.WithInputGuardrail(prefixGuardrail),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := a.Invoke(agent.NewContext(ctx), "Hello, what services do I have access to?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	t.Logf("Response: %s", result.Text)
	if !strings.Contains(strings.ToLower(result.Text), "premium") {
		t.Errorf("expected response to mention premium status, got: %s", result.Text)
	}
}

func TestIntegration_Guardrail_InputBlock(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	blockGuardrail := func(_ *agent.Context, msg string) (string, error) {
		if strings.Contains(strings.ToLower(msg), "password") {
			return "", errors.New("messages containing sensitive information are not allowed")
		}
		return msg, nil
	}

	a, err := agent.New(p, "You are a helpful assistant.", agent.WithInputGuardrail(blockGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := agent.NewContext(ctx)
	_, err = a.Invoke(c, "My password is hunter2")
	if err == nil {
		t.Fatal("expected guardrail error, got nil")
	}

	var guardrailErr *agent.GuardrailError
	if !errors.As(err, &guardrailErr) {
		t.Fatalf("expected *GuardrailError, got %T: %v", err, err)
	}
	if guardrailErr.Direction != "input" {
		t.Errorf("expected direction=input, got %s", guardrailErr.Direction)
	}
	t.Logf("Blocked as expected: %v", err)

	result, err := a.Invoke(c, "What is the capital of France?")
	if err != nil {
		t.Fatalf("expected clean message to pass, got: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Text), "paris") {
		t.Logf("Response: %s", result.Text)
	}
}

func TestIntegration_Guardrail_OutputTransform(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	disclaimerGuardrail := func(_ *agent.Context, response string) (string, error) {
		return response + "\n\n---\nDisclaimer: This is not financial advice.", nil
	}

	a, err := agent.New(p, "You are a financial assistant. Be brief.", agent.WithOutputGuardrail(disclaimerGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := a.Invoke(agent.NewContext(ctx), "Should I invest in index funds?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	t.Logf("Response: %s", result.Text)
	if !strings.Contains(result.Text, "Disclaimer: This is not financial advice.") {
		t.Errorf("expected disclaimer appended to response, got: %s", result.Text)
	}
}

func TestIntegration_Guardrail_OutputBlock(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	topicBlocker := func(_ *agent.Context, response string) (string, error) {
		lower := strings.ToLower(response)
		for _, word := range []string{"nuclear", "weapon", "explosive"} {
			if strings.Contains(lower, word) {
				return "", fmt.Errorf("response contains blocked topic: %s", word)
			}
		}
		return response, nil
	}

	a, err := agent.New(p, "You are a helpful assistant. Be brief.", agent.WithOutputGuardrail(topicBlocker))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := a.Invoke(agent.NewContext(ctx), "What is the capital of Japan?")
	if err != nil {
		t.Fatalf("safe question failed: %v", err)
	}
	t.Logf("Safe response: %s", result.Text)
	if !strings.Contains(strings.ToLower(result.Text), "tokyo") {
		t.Logf("Warning: expected Tokyo in response")
	}
}

func TestIntegration_Guardrail_ChainedGuardrails(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	callOrder := make([]string, 0)
	g1 := func(_ *agent.Context, msg string) (string, error) {
		callOrder = append(callOrder, "input-1")
		return strings.ToUpper(msg), nil
	}
	g2 := func(_ *agent.Context, msg string) (string, error) {
		callOrder = append(callOrder, "input-2")
		return msg + " [verified]", nil
	}
	g3 := func(_ *agent.Context, response string) (string, error) {
		callOrder = append(callOrder, "output-1")
		return response + " [reviewed]", nil
	}

	a, err := agent.New(
		p,
		"You are a helpful assistant. Be very brief — one sentence max.",
		agent.WithInputGuardrail(g1, g2),
		agent.WithOutputGuardrail(g3),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := a.Invoke(agent.NewContext(ctx), "hello")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	t.Logf("Response: %s", result.Text)
	t.Logf("Call order: %v", callOrder)
	if len(callOrder) < 3 {
		t.Errorf("expected at least 3 guardrail calls, got %d: %v", len(callOrder), callOrder)
	}
	if len(callOrder) >= 2 && (callOrder[0] != "input-1" || callOrder[1] != "input-2") {
		t.Errorf("expected input guardrails to run in order, got: %v", callOrder)
	}
	if !strings.HasSuffix(result.Text, "[reviewed]") {
		t.Errorf("expected output to end with [reviewed], got: %s", result.Text)
	}
}
