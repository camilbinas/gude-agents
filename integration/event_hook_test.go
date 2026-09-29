package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestIntegration_EventHook_FullLifecycle verifies that detailed lifecycle and
// tool events are emitted correctly during a real LLM invocation with tool calls.
func TestIntegration_EventHook_FullLifecycle(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type CalcInput struct {
		Expression string `json:"expression" description:"A math expression" required:"true"`
	}

	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in CalcInput) (string, error) {
		return "42", nil
	})

	a, err := agent.New(
		p,
		"You are a calculator. Always use the calculate tool for math. Be very brief.",
		agent.WithTools(calcTool),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	counts := lifecycleCounts{}
	c := agent.NewContext(ctx).WithDetailedEvents()
	var result *agent.Result
	for event, streamErr := range a.Stream(c, "What is 7 times 6?") {
		if streamErr != nil {
			t.Fatalf("Stream error: %v", streamErr)
		}
		switch event.Type {
		case agent.EventModelStart:
			counts.modelStartCount++
		case agent.EventModelEnd:
			counts.modelEndCount++
			counts.stopReasons = append(counts.stopReasons, event.Lifecycle.StopReason)
		case agent.EventToolStart:
			counts.toolStartCount++
			counts.toolNames = append(counts.toolNames, event.Tool.Name)
		case agent.EventToolEnd:
			counts.toolEndCount++
		case agent.EventEnd:
			result = event.Result
		}
	}
	if result == nil {
		t.Fatal("stream ended without a result")
	}
	if !strings.Contains(result.Text, "42") {
		t.Logf("Warning: expected '42' in response, got: %s", result.Text)
	}

	// Model start should fire at least twice (tool call + final response).
	if counts.modelStartCount < 2 {
		t.Errorf("expected model_start >= 2, got %d", counts.modelStartCount)
	}
	if counts.modelEndCount != counts.modelStartCount {
		t.Errorf("model_start=%d != model_end=%d", counts.modelStartCount, counts.modelEndCount)
	}
	if counts.toolStartCount < 1 {
		t.Errorf("expected tool_start >= 1, got %d", counts.toolStartCount)
	}
	if counts.toolEndCount != counts.toolStartCount {
		t.Errorf("tool_start=%d != tool_end=%d", counts.toolStartCount, counts.toolEndCount)
	}

	hasToolUse := false
	hasEndTurn := false
	for _, reason := range counts.stopReasons {
		if reason == "tool_use" {
			hasToolUse = true
		}
		if reason == "end_turn" {
			hasEndTurn = true
		}
	}
	if !hasToolUse {
		t.Error("expected at least one model_end with stop_reason=tool_use")
	}
	if !hasEndTurn {
		t.Error("expected at least one model_end with stop_reason=end_turn")
	}

	t.Logf("Events: modelStart=%d, modelEnd=%d, toolStart=%d, toolEnd=%d, stopReasons=%v",
		counts.modelStartCount, counts.modelEndCount, counts.toolStartCount, counts.toolEndCount, counts.stopReasons)
}

type lifecycleCounts struct {
	modelStartCount int
	modelEndCount   int
	toolStartCount  int
	toolEndCount    int
	stopReasons     []string
	toolNames       []string
}
