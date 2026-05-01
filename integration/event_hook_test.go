package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestIntegration_EventHook_FullLifecycle verifies that EventHook methods fire
// correctly during a real LLM invocation with tool calls.
func TestIntegration_EventHook_FullLifecycle(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type CalcInput struct {
		Expression string `json:"expression" description:"A math expression" required:"true"`
	}

	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in CalcInput) (string, error) {
		return "42", nil
	})

	a, err := agent.New(p,
		prompt.Text("You are a calculator. Always use the calculate tool for math. Be very brief."),
		[]tool.Tool{calcTool},
	)
	if err != nil {
		t.Fatal(err)
	}

	hook := &lifecycleHook{}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx).WithEventHook(hook)
	result, err := a.Invoke(c, "What is 7 times 6?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if !strings.Contains(result, "42") {
		t.Logf("Warning: expected '42' in response, got: %s", result)
	}

	// OnModelStart should fire at least twice (tool call + final response).
	if hook.modelStartCount < 2 {
		t.Errorf("expected OnModelStart >= 2, got %d", hook.modelStartCount)
	}
	if hook.modelEndCount != hook.modelStartCount {
		t.Errorf("OnModelStart=%d != OnModelEnd=%d", hook.modelStartCount, hook.modelEndCount)
	}
	// OnToolCallStart should fire at least once.
	if hook.toolStartCount < 1 {
		t.Errorf("expected OnToolCallStart >= 1, got %d", hook.toolStartCount)
	}
	if hook.toolEndCount != hook.toolStartCount {
		t.Errorf("OnToolCallStart=%d != OnToolCallEnd=%d", hook.toolStartCount, hook.toolEndCount)
	}

	// Verify stop reasons include both tool_use and end_turn.
	hasToolUse := false
	hasEndTurn := false
	for _, r := range hook.stopReasons {
		if r == "tool_use" {
			hasToolUse = true
		}
		if r == "end_turn" {
			hasEndTurn = true
		}
	}
	if !hasToolUse {
		t.Error("expected at least one OnModelEnd with stop_reason=tool_use")
	}
	if !hasEndTurn {
		t.Error("expected at least one OnModelEnd with stop_reason=end_turn")
	}

	t.Logf("EventHook: modelStart=%d, modelEnd=%d, toolStart=%d, toolEnd=%d, stopReasons=%v",
		hook.modelStartCount, hook.modelEndCount, hook.toolStartCount, hook.toolEndCount, hook.stopReasons)
}

// lifecycleHook tracks EventHook invocations for verification.
type lifecycleHook struct {
	agent.BaseEventHook
	modelStartCount int
	modelEndCount   int
	toolStartCount  int
	toolEndCount    int
	stopReasons     []string
	toolNames       []string
}

func (h *lifecycleHook) OnModelStart(_ *agent.Context) {
	h.modelStartCount++
}

func (h *lifecycleHook) OnModelEnd(_ *agent.Context, stopReason string) {
	h.modelEndCount++
	h.stopReasons = append(h.stopReasons, stopReason)
}

func (h *lifecycleHook) OnToolCallStart(_ *agent.Context, toolName string, _ json.RawMessage) {
	h.toolStartCount++
	h.toolNames = append(h.toolNames, toolName)
}

func (h *lifecycleHook) OnToolCallEnd(_ *agent.Context, _ string, _ string, _ error, _ time.Duration) {
	h.toolEndCount++
}
