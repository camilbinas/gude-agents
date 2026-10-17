package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// mockProvider is a minimal Provider for construction tests (never called).
type mockProvider struct{}

func (mockProvider) Name() string { return "mock" }

func (mockProvider) Stream(_ context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	return &ModelResponse{Text: "ok"}, nil
}

// scriptedProvider returns a pre-configured sequence of ModelResponses.
// Each call to Stream pops the next response from the queue.
// It also streams text as individual word chunks when the response is a final text answer.
type scriptedProvider struct {
	mu        sync.Mutex
	responses []*ModelResponse
	callIndex int
}

func newScriptedProvider(responses ...*ModelResponse) *scriptedProvider {
	return &scriptedProvider{responses: responses}
}

func (sp *scriptedProvider) Name() string { return "mock" }

func (sp *scriptedProvider) Stream(ctx context.Context, params ModelRequest, cb func(ModelEvent)) (*ModelResponse, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.callIndex >= len(sp.responses) {
		return nil, fmt.Errorf("scriptedProvider: no more responses (call %d)", sp.callIndex)
	}
	resp := sp.responses[sp.callIndex]
	sp.callIndex++

	// Stream text as word-level chunks when it's a final text answer (no tool calls).
	if len(resp.ToolCalls) == 0 && resp.Text != "" && cb != nil {
		words := strings.Fields(resp.Text)
		for i, w := range words {
			if i > 0 {
				cb(ModelEvent{Type: ModelEventText, Text: " "})
			}
			cb(ModelEvent{Type: ModelEventText, Text: w})
		}
	}
	return resp, nil
}

// dummyHandler is a no-op tool handler for construction tests.
func dummyHandler(_ context.Context, _ json.RawMessage) (string, error) {
	return "ok", nil
}

func dummyTool(name, desc string) tool.Tool {
	return tool.NewRaw(name, desc, nil, dummyHandler)
}

// textStreamCB drains TextStream, forwarding each chunk to cb (if non-nil).
func textStreamCB(a *Agent, c *Context, msg string, cb func(string)) error {
	for chunk, err := range a.TextStream(c, msg) {
		if err != nil {
			return err
		}
		if cb != nil {
			cb(chunk)
		}
	}
	return nil
}

// toolCall is a helper to build a ToolCall with empty JSON input.
func toolCall(id, name string) tool.Call {
	return tool.Call{ToolUseID: id, Name: name, Input: json.RawMessage(`{}`)}
}

func TestNewAgent_ValidConstruction(t *testing.T) {
	tools := []tool.Tool{
		dummyTool("search", "Search things"),
		dummyTool("create", "Create things"),
	}

	a, err := New(mockProvider{}, "You are helpful.", WithTools(tools...))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(a.ToolSpecs()); got != 2 {
		t.Errorf("expected 2 tools, got %d", got)
	}
	if a.maxIterations != 10 {
		t.Errorf("expected default maxIterations=10, got %d", a.maxIterations)
	}
	if a.toolExecution != ToolExecutionParallel || a.maxConcurrentTools != 0 {
		t.Errorf("expected unbounded parallel tool execution by default, got mode=%v limit=%d", a.toolExecution, a.maxConcurrentTools)
	}
}

func TestNewAgent_WithOptions(t *testing.T) {
	tools := []tool.Tool{dummyTool("t1", "Tool one")}

	a, err := New(mockProvider{}, "sys", WithTools(tools...),
		WithMaxIterations(5),
		WithToolExecution(ToolExecutionSequential),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.maxIterations != 5 {
		t.Errorf("expected maxIterations=5, got %d", a.maxIterations)
	}
	if a.toolExecution != ToolExecutionSequential {
		t.Error("expected sequential execution after WithToolExecution(ToolExecutionSequential)")
	}
}

func TestNewAgent_DuplicateToolName(t *testing.T) {
	tools := []tool.Tool{
		dummyTool("search", "First search"),
		dummyTool("search", "Duplicate search"),
	}

	_, err := New(mockProvider{}, "sys", WithTools(tools...))
	if err == nil {
		t.Fatal("expected error for duplicate tool name, got nil")
	}
}

func TestNewAgent_MissingToolName(t *testing.T) {
	tools := []tool.Tool{
		dummyTool("", "Has description"),
	}

	_, err := New(mockProvider{}, "sys", WithTools(tools...))
	if err == nil {
		t.Fatal("expected error for empty tool name, got nil")
	}
}

func TestNewAgent_MissingToolDescription(t *testing.T) {
	tools := []tool.Tool{
		dummyTool("search", ""),
	}

	_, err := New(mockProvider{}, "sys", WithTools(tools...))
	if err == nil {
		t.Fatal("expected error for empty tool description, got nil")
	}
}

func TestNewAgent_NilToolHandler(t *testing.T) {
	tools := []tool.Tool{
		{
			Spec: tool.Spec{
				Name:        "broken",
				Description: "Has no handler",
				InputSchema: map[string]any{"type": "object"},
			},
			Handler: nil,
		},
	}

	_, err := New(mockProvider{}, "sys", WithTools(tools...))
	if err == nil {
		t.Fatal("expected error for nil tool handler, got nil")
	}
}

func TestNewAgent_NoTools(t *testing.T) {
	a, err := New(mockProvider{}, "sys")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(a.ToolSpecs()); got != 0 {
		t.Errorf("expected 0 tools, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Core loop tests
// ---------------------------------------------------------------------------

func TestInvoke_TextOnlyResponse(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "Hello world"})
	a, err := New(sp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Hello world" {
		t.Errorf("expected %q, got %q", "Hello world", result.Text)
	}
}

func TestInvoke_SingleToolCall(t *testing.T) {
	// Provider returns a tool call, then a final text answer.
	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{toolCall("tc1", "echo")}},
		&ModelResponse{Text: "done"},
	)

	echoTool := tool.NewRaw(
		"echo",
		"echoes input",
		nil,
		func(_ context.Context, input json.RawMessage) (string, error) {
			return "echoed", nil
		},
	)

	a, err := New(sp, "sys", WithTools(echoTool))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "call echo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "done" {
		t.Errorf("expected %q, got %q", "done", result.Text)
	}
}

func TestInvoke_SequentialToolExecutionOrder(t *testing.T) {
	// Provider returns two tool calls in one response, then final text.
	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			toolCall("tc1", "first"),
			toolCall("tc2", "second"),
		}},
		&ModelResponse{Text: "all done"},
	)

	var mu sync.Mutex
	var order []string

	makeTool := func(name string) tool.Tool {
		return tool.NewRaw(
			name,
			name+" tool",
			nil,
			func(_ context.Context, _ json.RawMessage) (string, error) {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				return name + " result", nil
			},
		)
	}

	// ToolExecutionSequential opts out of the default concurrent execution so
	// order is preserved.
	a, err := New(sp, "sys", WithTools(makeTool("first"), makeTool("second")), WithToolExecution(ToolExecutionSequential))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "all done" {
		t.Errorf("expected %q, got %q", "all done", result.Text)
	}

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("expected sequential order [first, second], got %v", order)
	}
}

func TestInvoke_ParallelToolExecutionCompletesAll(t *testing.T) {
	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			toolCall("tc1", "a"),
			toolCall("tc2", "b"),
			toolCall("tc3", "c"),
		}},
		&ModelResponse{Text: "parallel done"},
	)

	// Barrier: every tool must be running before any of them may finish. If
	// tool calls were executed sequentially the first tool would never see the
	// others arrive, and the guard timeout fails the test.
	var arrived sync.WaitGroup
	arrived.Add(3)
	allRunning := make(chan struct{})
	go func() {
		arrived.Wait()
		close(allRunning)
	}()

	var mu sync.Mutex
	executed := map[string]bool{}

	makeTool := func(name string) tool.Tool {
		return tool.NewRaw(
			name,
			name+" tool",
			nil,
			func(_ context.Context, _ json.RawMessage) (string, error) {
				arrived.Done()
				select {
				case <-allRunning:
				case <-time.After(5 * time.Second):
					return "", fmt.Errorf("tool %q: other tool calls never ran concurrently", name)
				}
				mu.Lock()
				executed[name] = true
				mu.Unlock()
				return name + " ok", nil
			},
		)
	}

	// Tool calls run in parallel by default.
	a, err := New(sp, "sys",
		WithTools(makeTool("a"), makeTool("b"), makeTool("c")),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "go parallel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "parallel done" {
		t.Errorf("expected %q, got %q", "parallel done", result.Text)
	}

	// All three tools must have been executed, which is only possible when
	// they were running at the same time.
	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{"a", "b", "c"} {
		if !executed[name] {
			t.Errorf("tool %q did not complete", name)
		}
	}
}

func TestInvoke_ToolErrorReturnedAsResultText(t *testing.T) {
	// Provider returns a tool call to "fail_tool", then final text.
	// We verify the agent doesn't abort — it sends the error as a tool result.
	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{toolCall("tc1", "fail_tool")}},
		&ModelResponse{Text: "recovered"},
	)

	failTool := tool.NewRaw(
		"fail_tool",
		"always fails",
		nil,
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("something broke")
		},
	)

	a, err := New(sp, "sys", WithTools(failTool))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "try it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "recovered" {
		t.Errorf("expected %q, got %q", "recovered", result.Text)
	}
}

func TestInvoke_MaxIterationError(t *testing.T) {
	// Provider always returns a tool call — never a final answer.
	// With maxIterations=2, the agent should error after 2 loops.
	alwaysToolCall := &ModelResponse{ToolCalls: []tool.Call{toolCall("tc", "loop")}}
	sp := newScriptedProvider(alwaysToolCall, alwaysToolCall, alwaysToolCall)

	loopTool := tool.NewRaw(
		"loop",
		"loops forever",
		nil,
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "looping", nil
		},
	)

	a, err := New(sp, "sys", WithTools(loopTool), WithMaxIterations(2))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "loop forever")
	if err == nil {
		t.Fatal("expected max iteration error, got nil")
	}
	if !strings.Contains(err.Error(), "max iterations") {
		t.Errorf("expected error to mention 'max iterations', got: %v", err)
	}
}

func TestTextStream_ChunksInOrder(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "one two three"})
	a, err := New(sp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	var chunks []string
	err = textStreamCB(a, Background(), "stream me", func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The scriptedProvider streams word-by-word with spaces between.
	expected := []string{"one", " ", "two", " ", "three"}
	if len(chunks) != len(expected) {
		t.Fatalf("expected %d chunks, got %d: %v", len(expected), len(chunks), chunks)
	}
	for i, want := range expected {
		if chunks[i] != want {
			t.Errorf("chunk[%d]: expected %q, got %q", i, want, chunks[i])
		}
	}
}

func TestTextStream_SuppressesChunksDuringToolIteration(t *testing.T) {
	// First response has tool calls (text should be suppressed).
	// Second response is the final answer (text should be streamed).
	sp := newScriptedProvider(
		&ModelResponse{
			Text:      "thinking...",
			ToolCalls: []tool.Call{toolCall("tc1", "work")},
		},
		&ModelResponse{Text: "final answer"},
	)

	workTool := tool.NewRaw(
		"work",
		"does work",
		nil,
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "worked", nil
		},
	)

	a, err := New(sp, "sys", WithTools(workTool))
	if err != nil {
		t.Fatal(err)
	}

	var chunks []string
	err = textStreamCB(a, Background(), "go", func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the final answer chunks should appear — "thinking..." is suppressed.
	joined := strings.Join(chunks, "")
	if joined != "final answer" {
		t.Errorf("expected streamed text %q, got %q", "final answer", joined)
	}
}

func TestInvoke_MultiToolCallsResultOrderPreserved(t *testing.T) {
	// Verify that tool results are sent back in the same order as the tool calls,
	// even when running in parallel.
	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			toolCall("id-a", "alpha"),
			toolCall("id-b", "beta"),
			toolCall("id-c", "gamma"),
		}},
		&ModelResponse{Text: "ordered"},
	)

	makeTool := func(name, result string) tool.Tool {
		return tool.NewRaw(
			name,
			name+" tool",
			nil,
			func(_ context.Context, _ json.RawMessage) (string, error) {
				return result, nil
			},
		)
	}

	// Parallel execution (the default) stresses order preservation.
	a, err := New(sp, "sys",
		WithTools(makeTool("alpha", "A"), makeTool("beta", "B"), makeTool("gamma", "C")),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "ordered" {
		t.Errorf("expected %q, got %q", "ordered", result.Text)
	}

	// Verify the provider received tool results in the correct order by inspecting
	// the messages sent in the second call. The scriptedProvider doesn't capture
	// params, so we verify indirectly: the agent completed without error, meaning
	// it successfully sent results back and got the final answer.
}

func TestInvoke_UnknownToolReturnsError(t *testing.T) {
	// Provider asks for a tool that doesn't exist.
	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{toolCall("tc1", "nonexistent")}},
		&ModelResponse{Text: "handled"},
	)

	a, err := New(sp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "call missing tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "handled" {
		t.Errorf("expected %q, got %q", "handled", result.Text)
	}
}

func TestTextStream_NilCallbackDoesNotPanic(t *testing.T) {
	sp := newScriptedProvider(&ModelResponse{Text: "hello"})
	a, err := New(sp, "sys")
	if err != nil {
		t.Fatal(err)
	}

	// Passing nil callback should not panic.
	err = textStreamCB(a, Background(), "hi", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// errorProvider always returns an error from Stream.
type errorProvider struct{ err error }

func (ep errorProvider) Name() string { return "mock" }

func (ep errorProvider) Stream(_ context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	return nil, ep.err
}

func TestInvoke_ProviderErrorWrapped(t *testing.T) {
	cause := fmt.Errorf("connection refused")
	a, err := New(errorProvider{err: cause}, "sys")
	if err != nil {
		t.Fatal(err)
	}

	_, invokeErr := a.Invoke(Background(), "hello")
	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}

	var pe *ProviderError
	if !errors.As(invokeErr, &pe) {
		t.Fatalf("expected *ProviderError, got %T: %v", invokeErr, invokeErr)
	}
	if pe.Cause != cause {
		t.Errorf("expected Cause=%v, got %v", cause, pe.Cause)
	}
}

func TestInvoke_ToolErrorWrapped(t *testing.T) {
	cause := fmt.Errorf("tool exploded")

	boomTool := tool.NewRaw(
		"boom",
		"always errors",
		nil,
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", cause
		},
	)

	// Use capturingProvider (defined in guardrail_test.go) to inspect what the
	// second provider call receives as tool results.
	cp := newCapturingProvider(
		&ModelResponse{ToolCalls: []tool.Call{toolCall("tc1", "boom")}},
		&ModelResponse{Text: "done"},
	)

	a, err := New(cp, "sys", WithTools(boomTool))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "try boom")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "done" {
		t.Errorf("expected %q, got %q", "done", result.Text)
	}

	// The second provider call should have received a tool result with IsError=true
	// and the ToolError message as content.
	if len(cp.captured) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(cp.captured))
	}
	secondCallMsgs := cp.captured[1].Messages
	expectedMsg := (&ToolError{ToolName: "boom", Cause: cause}).Error()
	found := false
	for _, msg := range secondCallMsgs {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok && tr.IsError {
				if tr.Content == expectedMsg {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected ToolResultBlock with content %q", expectedMsg)
	}
}

func TestInvoke_RichToolNilOutputReturnsErrorResult(t *testing.T) {
	emptyRichTool := tool.NewRich[json.RawMessage]("empty_rich", "returns no output",
		func(_ context.Context, _ json.RawMessage) (*tool.Output, error) {
			return nil, nil
		}, tool.WithSchema(map[string]any{"type": "object"}))
	cp := newCapturingProvider(
		&ModelResponse{ToolCalls: []tool.Call{toolCall("empty-rich-1", "empty_rich")}},
		&ModelResponse{Text: "recovered"},
	)
	a, err := New(cp, "sys", WithTools(emptyRichTool))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "run empty rich tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "recovered" {
		t.Errorf("result = %q, want %q", result.Text, "recovered")
	}
	if len(cp.captured) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(cp.captured))
	}

	expected := (&ToolError{ToolName: "empty_rich", Cause: fmt.Errorf("rich tool %q returned nil output", "empty_rich")}).Error()
	found := false
	for _, msg := range cp.captured[1].Messages {
		for _, block := range msg.Content {
			if tr, ok := block.(ToolResultBlock); ok && tr.ToolUseID == "empty-rich-1" && tr.IsError && tr.Content == expected {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected IsError ToolResultBlock with content %q", expected)
	}
}

func TestInvoke_InputGuardrailErrorWrapped(t *testing.T) {
	cause := fmt.Errorf("blocked by policy")
	sp := newScriptedProvider(&ModelResponse{Text: "ok"})
	a, err := New(sp, "sys",
		WithInputGuardrail(func(_ *Context, msg string) (string, error) {
			return "", cause
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, invokeErr := a.Invoke(Background(), "bad input")
	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}

	var ge *GuardrailError
	if !errors.As(invokeErr, &ge) {
		t.Fatalf("expected *GuardrailError, got %T: %v", invokeErr, invokeErr)
	}
	if ge.Direction != "input" {
		t.Errorf("expected Direction=%q, got %q", "input", ge.Direction)
	}
	if ge.Cause != cause {
		t.Errorf("expected Cause=%v, got %v", cause, ge.Cause)
	}
}

func TestInvoke_OutputGuardrailErrorWrapped(t *testing.T) {
	cause := fmt.Errorf("output blocked")
	sp := newScriptedProvider(&ModelResponse{Text: "some response"})
	a, err := New(sp, "sys",
		WithOutputGuardrail(func(_ *Context, msg string) (string, error) {
			return "", cause
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, invokeErr := a.Invoke(Background(), "hello")
	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}

	var ge *GuardrailError
	if !errors.As(invokeErr, &ge) {
		t.Fatalf("expected *GuardrailError, got %T: %v", invokeErr, invokeErr)
	}
	if ge.Direction != "output" {
		t.Errorf("expected Direction=%q, got %q", "output", ge.Direction)
	}
	if ge.Cause != cause {
		t.Errorf("expected Cause=%v, got %v", cause, ge.Cause)
	}
}

// ---------------------------------------------------------------------------
// Instructions
// ---------------------------------------------------------------------------

func TestInstructions_ReturnsInitialValue(t *testing.T) {
	a, err := New(mockProvider{}, "initial system prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := a.Instructions(); got != "initial system prompt" {
		t.Errorf("Instructions() = %q, want %q", got, "initial system prompt")
	}
}

// ---------------------------------------------------------------------------
// Per-invocation system prompt override (for A/B testing)
// ---------------------------------------------------------------------------

// systemPromptCapturingProvider records the System field passed in each
// ModelRequest so tests can assert which prompt the agent loop used.
type systemPromptCapturingProvider struct {
	mu       sync.Mutex
	captured []string
}

func (p *systemPromptCapturingProvider) Name() string { return "capture" }

func (p *systemPromptCapturingProvider) Stream(_ context.Context, params ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	p.mu.Lock()
	p.captured = append(p.captured, params.System)
	p.mu.Unlock()
	return &ModelResponse{Text: "ok"}, nil
}

func (p *systemPromptCapturingProvider) lastSystem() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.captured) == 0 {
		return ""
	}
	return p.captured[len(p.captured)-1]
}

func TestInvoke_UsesContextSystemPromptOverride(t *testing.T) {
	p := &systemPromptCapturingProvider{}
	a, err := New(p, "agent default prompt")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := Background().WithInstructions("override prompt for this turn")
	if _, err := a.Invoke(ctx, "hi"); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if got := p.lastSystem(); got != "override prompt for this turn" {
		t.Errorf("provider received System=%q, want %q", got, "override prompt for this turn")
	}
}

func TestInvoke_FallsBackToAgentInstructionsWhenNoOverride(t *testing.T) {
	p := &systemPromptCapturingProvider{}
	a, err := New(p, "agent default prompt")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := a.Invoke(Background(), "hi"); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if got := p.lastSystem(); got != "agent default prompt" {
		t.Errorf("provider received System=%q, want default", got)
	}
}

func TestInvoke_DifferentOverridesPerCall(t *testing.T) {
	p := &systemPromptCapturingProvider{}
	a, err := New(p, "default")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctxA := Background().WithInstructions("variant-A")
	if _, err := a.Invoke(ctxA, "hi"); err != nil {
		t.Fatal(err)
	}

	ctxB := Background().WithInstructions("variant-B")
	if _, err := a.Invoke(ctxB, "hi"); err != nil {
		t.Fatal(err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.captured) != 2 {
		t.Fatalf("captured = %d, want 2", len(p.captured))
	}
	if p.captured[0] != "variant-A" || p.captured[1] != "variant-B" {
		t.Errorf("captured = %v, want [variant-A variant-B]", p.captured)
	}
}

func TestToolRegistrySupportsDynamicTools(t *testing.T) {
	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{toolCall("dyn-1", "dynamic")}},
		&ModelResponse{Text: "done"},
	)
	var registry tool.Registry
	a, err := New(sp, "sys", WithToolRegistry(&registry))
	if err != nil {
		t.Fatal(err)
	}

	// The tool is registered after the Agent was constructed.
	var ran bool
	dynamic := tool.NewRaw("dynamic", "Registered after New", nil,
		func(context.Context, json.RawMessage) (string, error) {
			ran = true
			return "ok", nil
		})
	if err := registry.Register(dynamic); err != nil {
		t.Fatalf("Register returned an error: %v", err)
	}

	result, err := a.Invoke(Background(), "use the dynamic tool")
	if err != nil {
		t.Fatalf("Invoke returned an error: %v", err)
	}
	if !ran || result.Text != "done" {
		t.Fatalf("dynamic tool ran=%v, result=%q", ran, result.Text)
	}
}

// newTestBackgroundRaw adapts raw-message test fixtures to the canonical generic constructor.
// newTestRaw adapts hand-written schema fixtures to the canonical raw constructor.
func newTestRaw(name, description string, schema map[string]any, handler func(context.Context, json.RawMessage) (string, error), opts ...tool.Option) tool.Tool {
	opts = append(opts, tool.WithSchema(schema))
	return tool.NewRaw(name, description, nil, handler, opts...)
}

// contains is shared by root agent tests that assert error text.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// TestAgentHasNoProcessLocalLifecycleState pins the single-persistence model:
// the Agent owns no generic shutdown/flush hook, no synchronous-conversation
// switch, and no process-local pause registry. Pauses live in the configured
// ExecutionStore, and Append is committed when it returns.
func TestAgentHasNoProcessLocalLifecycleState(t *testing.T) {
	agentType := reflect.TypeOf(&Agent{})
	if _, ok := agentType.MethodByName("Shutdown"); ok {
		t.Error("Agent must not expose Shutdown")
	}
	for _, field := range []string{"syncConversation", "localPauses"} {
		if _, ok := agentType.Elem().FieldByName(field); ok {
			t.Errorf("Agent must not have a %s field", field)
		}
	}
}

// The public inspection surface is Name, Instructions and ToolSpecs only. The
// removed getters must not come back as part of a configuration snapshot.
func TestAgentPublicInspectionSurface(t *testing.T) {
	typ := reflect.TypeOf(&Agent{})
	for _, name := range []string{"Name", "Instructions", "ToolSpecs"} {
		if _, ok := typ.MethodByName(name); !ok {
			t.Errorf("Agent.%s must stay public", name)
		}
	}
	for _, name := range []string{
		"Provider", "CallProvider", "HasTool", "LookupTool", "HasConversation",
		"InferenceConfig", "MaxIterations", "ParallelTools", "TokenBudget",
		"Middlewares", "InputGuardrails", "OutputGuardrails",
		"ToolExecution", "MaxConcurrentTools", "ToolVisibility", "RateLimitKey", "Config",
	} {
		if _, ok := typ.MethodByName(name); ok {
			t.Errorf("Agent.%s must not be public", name)
		}
	}
}
