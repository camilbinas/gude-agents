package vllm

// Opt-in live smoke tests against the vLLM server. They are skipped
// unless TEST_LIVE_INFERENCE is exactly "1", and then require TEST_MODEL_ID.
// No default model is chosen. Credentials or TEST_MODEL_ID alone never enable
// them. Run from this module's directory:
//
//	TEST_LIVE_INFERENCE=1 TEST_MODEL_ID=<model> go test -run Live -v ./...
//
// Image/PDF checks are intentionally omitted: support depends on the served
// model, not the adapter. The server is taken from VLLM_BASE_URL (or localhost).
//
// The tests assert structure and ordering, never exact model wording.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

const liveTimeout = 2 * time.Minute

// liveProvider skips unless live tests are enabled, fails if the model ID is
// missing, and only then constructs the provider.
func liveProvider(t *testing.T) *Provider {
	t.Helper()
	if os.Getenv("TEST_LIVE_INFERENCE") != "1" {
		t.Skip("live test skipped: set TEST_LIVE_INFERENCE=1 (and TEST_MODEL_ID) to run")
	}
	model := os.Getenv("TEST_MODEL_ID")
	if model == "" {
		t.Fatalf("TEST_LIVE_INFERENCE=1 requires TEST_MODEL_ID to be set; no default model is chosen")
	}
	p, err := New(model)
	if err != nil {
		t.Fatalf("New(%q): %v", model, err)
	}
	return p
}

func liveContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	t.Cleanup(cancel)
	return ctx
}

func liveUser(blocks ...agent.ContentBlock) agent.Message {
	return agent.Message{Role: agent.RoleUser, Content: blocks}
}

func liveRequireUsage(t *testing.T, resp *agent.ModelResponse) {
	t.Helper()
	if resp.Usage.InputTokens <= 0 || resp.Usage.OutputTokens <= 0 {
		t.Errorf("usage not populated: %+v", resp.Usage)
	}
}

func liveWeatherTool() tool.Spec {
	return tool.Spec{
		Name:        "get_weather",
		Description: "Get the current weather for a city.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "City name"},
			},
			"required": []string{"city"},
		},
	}
}

func TestLive_Converse(t *testing.T) {
	p := liveProvider(t)
	resp, err := p.Stream(liveContext(t), agent.ModelRequest{
		Messages: []agent.Message{liveUser(agent.TextBlock{Text: "Say hello in one short sentence."})},
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if strings.TrimSpace(resp.Text) == "" {
		t.Error("response text is empty")
	}
	liveRequireUsage(t, resp)
}

func TestLive_Stream(t *testing.T) {
	p := liveProvider(t)
	var returned atomic.Bool
	var late atomic.Bool
	var chunks []string
	resp, err := p.Stream(liveContext(t), agent.ModelRequest{
		Messages: []agent.Message{liveUser(agent.TextBlock{Text: "Count from one to five in words."})},
	}, func(ev agent.ModelEvent) {
		if returned.Load() {
			late.Store(true)
		}
		if ev.Type == agent.ModelEventText {
			chunks = append(chunks, ev.Text)
		}
	})
	returned.Store(true)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(chunks) == 0 || strings.TrimSpace(strings.Join(chunks, "")) == "" {
		t.Error("no text events were emitted before the stream ended")
	}
	if late.Load() {
		t.Error("an event was emitted after Stream returned")
	}
	if strings.TrimSpace(resp.Text) == "" {
		t.Error("final response text is empty")
	}
	liveRequireUsage(t, resp)
}

func TestLive_ToolUse(t *testing.T) {
	p := liveProvider(t)
	if p.Capabilities().ToolUse == agent.Unsupported {
		t.Skipf("model %q is catalogued as not supporting tool use", p.ModelID())
	}
	ctx := liveContext(t)
	tools := []tool.Spec{liveWeatherTool()}
	user := liveUser(agent.TextBlock{Text: "What is the weather in Paris? Use the get_weather tool."})
	resp, err := p.Stream(ctx, agent.ModelRequest{Messages: []agent.Message{user}, Tools: tools}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(resp.ToolCalls) == 0 {
		t.Fatalf("no tool call requested (text=%q)", resp.Text)
	}
	call := resp.ToolCalls[0]
	if call.Name != "get_weather" {
		t.Fatalf("tool call name = %q, want get_weather", call.Name)
	}
	if call.ToolUseID == "" {
		t.Error("tool call has no ID")
	}
	var input struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		t.Fatalf("tool input is not valid JSON for the schema: %v (%s)", err, call.Input)
	}
	if input.City == "" {
		t.Errorf("tool input has no city: %s", call.Input)
	}
	liveRequireUsage(t, resp)

	// Return a tool result and expect the model to answer in text.
	resp2, err := p.Stream(ctx, agent.ModelRequest{
		Messages: []agent.Message{
			user,
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.ToolUseBlock{ToolUseID: call.ToolUseID, Name: call.Name, Input: call.Input}}},
			liveUser(agent.ToolResultBlock{ToolUseID: call.ToolUseID, Content: "18C and sunny"}),
		},
		Tools: tools,
	}, nil)
	if err != nil {
		t.Fatalf("Stream with tool result: %v", err)
	}
	if strings.TrimSpace(resp2.Text) == "" {
		t.Error("no text after returning the tool result")
	}
}
