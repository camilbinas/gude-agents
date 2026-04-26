package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

func TestProperty_AgentTokenAccumulation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random number of provider calls (1-5), each with random usage.
		numCalls := rapid.IntRange(1, 5).Draw(t, "numCalls")
		responses := make([]*ProviderResponse, numCalls)
		var expectedInput, expectedOutput int

		for i := 0; i < numCalls-1; i++ {
			inputTok := rapid.IntRange(0, 1000).Draw(t, "inputTokens")
			outputTok := rapid.IntRange(0, 1000).Draw(t, "outputTokens")
			expectedInput += inputTok
			expectedOutput += outputTok
			// Intermediate calls return tool calls to keep the loop going.
			responses[i] = &ProviderResponse{
				ToolCalls: []tool.Call{toolCall("tc", "dummy")},
				Usage:     TokenUsage{InputTokens: inputTok, OutputTokens: outputTok},
			}
		}

		// Final call returns text (ends the loop).
		lastInput := rapid.IntRange(0, 1000).Draw(t, "lastInputTokens")
		lastOutput := rapid.IntRange(0, 1000).Draw(t, "lastOutputTokens")
		expectedInput += lastInput
		expectedOutput += lastOutput
		responses[numCalls-1] = &ProviderResponse{
			Text:  "done",
			Usage: TokenUsage{InputTokens: lastInput, OutputTokens: lastOutput},
		}

		sp := newScriptedProvider(responses...)
		tools := []tool.Tool{dummyTool("dummy", "dummy tool")}
		a, err := New(sp, prompt.Text("sys"), tools)
		if err != nil {
			t.Fatal(err)
		}

		_, usage, err := a.Invoke(context.Background(), "go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if usage.InputTokens != expectedInput {
			t.Errorf("InputTokens: expected %d, got %d", expectedInput, usage.InputTokens)
		}
		if usage.OutputTokens != expectedOutput {
			t.Errorf("OutputTokens: expected %d, got %d", expectedOutput, usage.OutputTokens)
		}
	})
}

func TestProperty_AgentTokenResetBetweenInvocations(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		input1 := rapid.IntRange(1, 500).Draw(t, "input1")
		output1 := rapid.IntRange(1, 500).Draw(t, "output1")
		input2 := rapid.IntRange(1, 500).Draw(t, "input2")
		output2 := rapid.IntRange(1, 500).Draw(t, "output2")

		sp := newScriptedProvider(
			&ProviderResponse{
				Text:  "first",
				Usage: TokenUsage{InputTokens: input1, OutputTokens: output1},
			},
			&ProviderResponse{
				Text:  "second",
				Usage: TokenUsage{InputTokens: input2, OutputTokens: output2},
			},
		)

		a, err := New(sp, prompt.Text("sys"), nil)
		if err != nil {
			t.Fatal(err)
		}

		// First invocation.
		_, usage1, err := a.Invoke(context.Background(), "first")
		if err != nil {
			t.Fatalf("first invoke: %v", err)
		}

		// Second invocation.
		_, usage2, err := a.Invoke(context.Background(), "second")
		if err != nil {
			t.Fatalf("second invoke: %v", err)
		}

		// Usage from second invocation must reflect only the second call.
		if usage1.InputTokens != input1 || usage1.OutputTokens != output1 {
			t.Errorf("first usage: expected (%d, %d), got (%d, %d)",
				input1, output1, usage1.InputTokens, usage1.OutputTokens)
		}
		if usage2.InputTokens != input2 || usage2.OutputTokens != output2 {
			t.Errorf("second usage: expected (%d, %d), got (%d, %d)",
				input2, output2, usage2.InputTokens, usage2.OutputTokens)
		}
	})
}

func TestProperty_AgentBudgetEnforcement(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a budget and a sequence of provider calls.
		budget := rapid.IntRange(10, 500).Draw(t, "budget")
		numCalls := rapid.IntRange(1, 5).Draw(t, "numCalls")

		responses := make([]*ProviderResponse, numCalls+1) // +1 for potential final text
		var cumulativeTotal int
		expectedAbortIdx := -1

		for i := 0; i < numCalls; i++ {
			inputTok := rapid.IntRange(1, 200).Draw(t, "inputTokens")
			outputTok := rapid.IntRange(1, 200).Draw(t, "outputTokens")
			cumulativeTotal += inputTok + outputTok

			if expectedAbortIdx == -1 && cumulativeTotal > budget {
				expectedAbortIdx = i
			}

			// Use tool calls to keep the loop going.
			responses[i] = &ProviderResponse{
				ToolCalls: []tool.Call{toolCall("tc", "dummy")},
				Usage:     TokenUsage{InputTokens: inputTok, OutputTokens: outputTok},
			}
		}
		// Final text response in case budget is never exceeded.
		responses[numCalls] = &ProviderResponse{
			Text:  "done",
			Usage: TokenUsage{InputTokens: 0, OutputTokens: 0},
		}

		sp := newScriptedProvider(responses...)
		tools := []tool.Tool{dummyTool("dummy", "dummy tool")}
		a, err := New(sp, prompt.Text("sys"), tools, WithTokenBudget(budget))
		if err != nil {
			t.Fatal(err)
		}

		_, _, err = a.Invoke(context.Background(), "go")

		if expectedAbortIdx >= 0 {
			// Budget should have been exceeded.
			if !errors.Is(err, ErrTokenBudgetExceeded) {
				t.Errorf("expected ErrTokenBudgetExceeded, got: %v", err)
			}
		} else {
			// Budget was not exceeded.
			if err != nil {
				t.Errorf("expected no error (budget not exceeded), got: %v", err)
			}
		}
	})
}

func TestAgent_NoBudget_DoesNotAbort(t *testing.T) {
	// Provider returns large token usage but no budget is set — should succeed.
	sp := newScriptedProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{toolCall("tc", "dummy")},
			Usage:     TokenUsage{InputTokens: 50000, OutputTokens: 50000},
		},
		&ProviderResponse{
			Text:  "done",
			Usage: TokenUsage{InputTokens: 50000, OutputTokens: 50000},
		},
	)

	tools := []tool.Tool{dummyTool("dummy", "dummy tool")}
	a, err := New(sp, prompt.Text("sys"), tools) // No WithTokenBudget
	if err != nil {
		t.Fatal(err)
	}

	result, usage, err := a.Invoke(context.Background(), "go")
	if err != nil {
		t.Fatalf("expected no error without budget, got: %v", err)
	}
	if result != "done" {
		t.Errorf("expected %q, got %q", "done", result)
	}
	// Usage should still be accumulated even without a budget.
	if usage.InputTokens != 100000 {
		t.Errorf("expected InputTokens=100000, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 100000 {
		t.Errorf("expected OutputTokens=100000, got %d", usage.OutputTokens)
	}
}

func TestAgent_ZeroBudget_DoesNotAbort(t *testing.T) {
	// Explicitly setting budget to 0 should behave the same as no budget.
	sp := newScriptedProvider(
		&ProviderResponse{
			Text:  "ok",
			Usage: TokenUsage{InputTokens: 9999, OutputTokens: 9999},
		},
	)

	a, err := New(sp, prompt.Text("sys"), nil, WithTokenBudget(0))
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("expected no error with zero budget, got: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected %q, got %q", "ok", result)
	}
}
