package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// recordingEventHook records all EventHook calls for test assertions.
type recordingEventHook struct {
	mu sync.Mutex

	toolStarts  []toolStartEvent
	toolEnds    []toolEndEvent
	thinkings   []thinkingEvent
	modelStarts int
	modelEnds   []modelEndEvent
}

type toolStartEvent struct {
	toolName string
	input    json.RawMessage
}

type toolEndEvent struct {
	toolName string
	output   string
	err      error
	duration time.Duration
}

type thinkingEvent struct {
	chunk string
}

type modelEndEvent struct {
	stopReason string
}

func (r *recordingEventHook) OnToolCallStart(ctx context.Context, toolName string, input json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolStarts = append(r.toolStarts, toolStartEvent{toolName: toolName, input: input})
}

func (r *recordingEventHook) OnToolCallEnd(ctx context.Context, toolName string, output string, err error, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolEnds = append(r.toolEnds, toolEndEvent{toolName: toolName, output: output, err: err, duration: duration})
}

func (r *recordingEventHook) OnThinking(ctx context.Context, chunk string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.thinkings = append(r.thinkings, thinkingEvent{chunk: chunk})
}

func (r *recordingEventHook) OnModelStart(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modelStarts++
}

func (r *recordingEventHook) OnModelEnd(ctx context.Context, stopReason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modelEnds = append(r.modelEnds, modelEndEvent{stopReason: stopReason})
}

// ---------------------------------------------------------------------------
// 4.1: Context-based EventHook — WithEventHook/eventHookFromContext
// ---------------------------------------------------------------------------

func TestEventHook_ContextNilByDefault(t *testing.T) {
	ctx := context.Background()
	if eventHookFromContext(ctx) != nil {
		t.Error("expected nil EventHook from plain context")
	}
}

func TestEventHook_ContextRoundTrip(t *testing.T) {
	hook := &recordingEventHook{}
	ctx := WithEventHook(context.Background(), hook)

	got := eventHookFromContext(ctx)
	if got != hook {
		t.Error("expected eventHookFromContext to return the hook set via WithEventHook")
	}
}

func TestEventHook_BaseEventHookCompiles(t *testing.T) {
	// Verify BaseEventHook satisfies EventHook interface.
	var _ EventHook = BaseEventHook{}
	var _ EventHook = &BaseEventHook{}
}

// ---------------------------------------------------------------------------
// 4.2: Nil EventHook dispatch safety (no panics)
// ---------------------------------------------------------------------------

func TestEventHook_NilDispatchSafety(t *testing.T) {
	h := hooks{event: nil}
	ctx := context.Background()

	_, tf := h.onToolStart(ctx, "some_tool", json.RawMessage(`{"key":"value"}`))
	tf.finish(nil, "output")
	tf.finish(fmt.Errorf("some error"), "")

	_, pf := h.onProviderCallStart(ctx, ProviderCallParams{}, "model-id")
	pf.finish(nil, TokenUsage{}, 0, "text")
	pf.finish(fmt.Errorf("provider error"), TokenUsage{}, 0, "")
	pf.finish(nil, TokenUsage{}, 2, "")
}

// ---------------------------------------------------------------------------
// 4.3: OnToolCallStart receiving correct tool name and input
// ---------------------------------------------------------------------------

func TestEventHook_OnToolCallStart(t *testing.T) {
	hook := &recordingEventHook{}
	h := hooks{event: hook}
	ctx := context.Background()

	input := json.RawMessage(`{"query":"hello world"}`)
	_, tf := h.onToolStart(ctx, "web_search", input)
	tf.finish(nil, "result")

	hook.mu.Lock()
	defer hook.mu.Unlock()

	if len(hook.toolStarts) != 1 {
		t.Fatalf("expected 1 tool start event, got %d", len(hook.toolStarts))
	}
	ev := hook.toolStarts[0]
	if ev.toolName != "web_search" {
		t.Errorf("expected tool name %q, got %q", "web_search", ev.toolName)
	}
	if string(ev.input) != string(input) {
		t.Errorf("expected input %s, got %s", string(input), string(ev.input))
	}
}

// ---------------------------------------------------------------------------
// 4.4: OnToolCallEnd receiving correct output on success and error on failure
// ---------------------------------------------------------------------------

func TestEventHook_OnToolCallEnd_Success(t *testing.T) {
	hook := &recordingEventHook{}
	h := hooks{event: hook}
	ctx := context.Background()

	_, tf := h.onToolStart(ctx, "calculator", json.RawMessage(`{"expr":"2+2"}`))
	time.Sleep(5 * time.Millisecond)
	tf.finish(nil, "4")

	hook.mu.Lock()
	defer hook.mu.Unlock()

	if len(hook.toolEnds) != 1 {
		t.Fatalf("expected 1 tool end event, got %d", len(hook.toolEnds))
	}
	ev := hook.toolEnds[0]
	if ev.toolName != "calculator" {
		t.Errorf("expected tool name %q, got %q", "calculator", ev.toolName)
	}
	if ev.output != "4" {
		t.Errorf("expected output %q, got %q", "4", ev.output)
	}
	if ev.err != nil {
		t.Errorf("expected nil error, got %v", ev.err)
	}
	if ev.duration <= 0 {
		t.Errorf("expected positive duration, got %v", ev.duration)
	}
}

func TestEventHook_OnToolCallEnd_Error(t *testing.T) {
	hook := &recordingEventHook{}
	h := hooks{event: hook}
	ctx := context.Background()

	toolErr := fmt.Errorf("division by zero")
	_, tf := h.onToolStart(ctx, "calculator", json.RawMessage(`{"expr":"1/0"}`))
	time.Sleep(5 * time.Millisecond)
	tf.finish(toolErr, "")

	hook.mu.Lock()
	defer hook.mu.Unlock()

	if len(hook.toolEnds) != 1 {
		t.Fatalf("expected 1 tool end event, got %d", len(hook.toolEnds))
	}
	ev := hook.toolEnds[0]
	if ev.output != "" {
		t.Errorf("expected empty output, got %q", ev.output)
	}
	if ev.err != toolErr {
		t.Errorf("expected error %v, got %v", toolErr, ev.err)
	}
	if ev.duration <= 0 {
		t.Errorf("expected positive duration, got %v", ev.duration)
	}
}

// ---------------------------------------------------------------------------
// 4.5: OnModelStart/OnModelEnd with correct stop reasons
// ---------------------------------------------------------------------------

func TestEventHook_OnModelEnd_EndTurn(t *testing.T) {
	hook := &recordingEventHook{}
	h := hooks{event: hook}
	ctx := context.Background()

	_, pf := h.onProviderCallStart(ctx, ProviderCallParams{}, "model-id")
	pf.finish(nil, TokenUsage{}, 0, "final answer")

	hook.mu.Lock()
	defer hook.mu.Unlock()

	if hook.modelStarts != 1 {
		t.Errorf("expected 1 model start, got %d", hook.modelStarts)
	}
	if len(hook.modelEnds) != 1 {
		t.Fatalf("expected 1 model end event, got %d", len(hook.modelEnds))
	}
	if hook.modelEnds[0].stopReason != StopReasonEndTurn {
		t.Errorf("expected stop reason %q, got %q", StopReasonEndTurn, hook.modelEnds[0].stopReason)
	}
}

func TestEventHook_OnModelEnd_ToolUse(t *testing.T) {
	hook := &recordingEventHook{}
	h := hooks{event: hook}
	ctx := context.Background()

	_, pf := h.onProviderCallStart(ctx, ProviderCallParams{}, "model-id")
	pf.finish(nil, TokenUsage{}, 3, "")

	hook.mu.Lock()
	defer hook.mu.Unlock()

	if hook.modelEnds[0].stopReason != StopReasonToolUse {
		t.Errorf("expected stop reason %q, got %q", StopReasonToolUse, hook.modelEnds[0].stopReason)
	}
}

func TestEventHook_OnModelEnd_Error(t *testing.T) {
	hook := &recordingEventHook{}
	h := hooks{event: hook}
	ctx := context.Background()

	_, pf := h.onProviderCallStart(ctx, ProviderCallParams{}, "model-id")
	pf.finish(fmt.Errorf("timeout"), TokenUsage{}, 0, "")

	hook.mu.Lock()
	defer hook.mu.Unlock()

	if hook.modelEnds[0].stopReason != StopReasonError {
		t.Errorf("expected stop reason %q, got %q", StopReasonError, hook.modelEnds[0].stopReason)
	}
}

// ---------------------------------------------------------------------------
// 4.6: Thinking chunk forwarding to EventHook.OnThinking
// ---------------------------------------------------------------------------

// thinkingProvider invokes the ThinkingCallback during ConverseStream.
type thinkingProvider struct {
	chunks   []string
	response *ProviderResponse
}

func (tp *thinkingProvider) Converse(_ context.Context, params ConverseParams) (*ProviderResponse, error) {
	return tp.response, nil
}

func (tp *thinkingProvider) ConverseStream(_ context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	if params.ThinkingCallback != nil {
		for _, chunk := range tp.chunks {
			params.ThinkingCallback(chunk)
		}
	}
	if cb != nil && tp.response.Text != "" {
		cb(tp.response.Text)
	}
	return tp.response, nil
}

func TestEventHook_ThinkingForwarding(t *testing.T) {
	hook := &recordingEventHook{}
	provider := &thinkingProvider{
		chunks:   []string{"Let me think...", "I see the answer."},
		response: &ProviderResponse{Text: "42"},
	}

	a, err := New(provider, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := WithEventHook(context.Background(), hook)
	_, _, err = a.Invoke(ctx, "what is the answer?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hook.mu.Lock()
	defer hook.mu.Unlock()

	if len(hook.thinkings) != 2 {
		t.Fatalf("expected 2 thinking events, got %d", len(hook.thinkings))
	}
	if hook.thinkings[0].chunk != "Let me think..." {
		t.Errorf("expected first chunk %q, got %q", "Let me think...", hook.thinkings[0].chunk)
	}
	if hook.thinkings[1].chunk != "I see the answer." {
		t.Errorf("expected second chunk %q, got %q", "I see the answer.", hook.thinkings[1].chunk)
	}
}

// ---------------------------------------------------------------------------
// 4.7: No EventHook in context — thinking chunks are discarded silently
// ---------------------------------------------------------------------------

func TestEventHook_NoHookNoThinking(t *testing.T) {
	provider := &thinkingProvider{
		chunks:   []string{"reasoning step 1", "reasoning step 2"},
		response: &ProviderResponse{Text: "done"},
	}

	a, err := New(provider, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	// No EventHook in context — should not panic.
	_, _, err = a.Invoke(context.Background(), "think about this")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 4.8: Parallel tool execution dispatching events independently
// ---------------------------------------------------------------------------

func TestEventHook_ParallelToolDispatch(t *testing.T) {
	hook := &recordingEventHook{}

	sp := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{
			toolCall("tc1", "alpha"),
			toolCall("tc2", "beta"),
			toolCall("tc3", "gamma"),
		}},
		&ProviderResponse{Text: "all done"},
	)

	makeTool := func(name string, delay time.Duration) tool.Tool {
		return tool.NewRaw(name, name+" tool", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				time.Sleep(delay)
				return name + " result", nil
			})
	}

	a, err := New(sp, prompt.Text("sys"),
		[]tool.Tool{
			makeTool("alpha", 10*time.Millisecond),
			makeTool("beta", 20*time.Millisecond),
			makeTool("gamma", 15*time.Millisecond),
		},
		WithParallelToolExecution(),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx := WithEventHook(context.Background(), hook)
	_, _, err = a.Invoke(ctx, "run all tools")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hook.mu.Lock()
	defer hook.mu.Unlock()

	if len(hook.toolStarts) != 3 {
		t.Errorf("expected 3 tool start events, got %d", len(hook.toolStarts))
	}
	if len(hook.toolEnds) != 3 {
		t.Errorf("expected 3 tool end events, got %d", len(hook.toolEnds))
	}

	startNames := map[string]bool{}
	for _, ev := range hook.toolStarts {
		startNames[ev.toolName] = true
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !startNames[name] {
			t.Errorf("missing tool start event for %q", name)
		}
	}

	endNames := map[string]string{}
	for _, ev := range hook.toolEnds {
		endNames[ev.toolName] = ev.output
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		expected := name + " result"
		if endNames[name] != expected {
			t.Errorf("tool %q: expected output %q, got %q", name, expected, endNames[name])
		}
	}

	for _, ev := range hook.toolEnds {
		if ev.err != nil {
			t.Errorf("tool %q: unexpected error %v", ev.toolName, ev.err)
		}
	}
}

// ---------------------------------------------------------------------------
// deriveStopReason unit test
// ---------------------------------------------------------------------------

func TestDeriveStopReason(t *testing.T) {
	tests := []struct {
		name           string
		toolCallCount  int
		err            error
		expectedReason string
	}{
		{"no tools no error", 0, nil, StopReasonEndTurn},
		{"with tool calls", 2, nil, StopReasonToolUse},
		{"with error", 0, errors.New("fail"), StopReasonError},
		{"error takes precedence over tool calls", 3, errors.New("fail"), StopReasonError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveStopReason(tt.toolCallCount, tt.err)
			if got != tt.expectedReason {
				t.Errorf("deriveStopReason(%d, %v) = %q, want %q", tt.toolCallCount, tt.err, got, tt.expectedReason)
			}
		})
	}
}
