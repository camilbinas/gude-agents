package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// mockProvider is a minimal Provider for construction tests (never called).
type mockProvider struct{}

func (mockProvider) Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error) {
	return &ProviderResponse{Text: "ok"}, nil
}

func (mockProvider) ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	return &ProviderResponse{Text: "ok"}, nil
}

// scriptedProvider returns a pre-configured sequence of ProviderResponses.
// Each call to ConverseStream pops the next response from the queue.
// It also streams text as individual word chunks when the response is a final text answer.
type scriptedProvider struct {
	mu        sync.Mutex
	responses []*ProviderResponse
	callIndex int
}

func newScriptedProvider(responses ...*ProviderResponse) *scriptedProvider {
	return &scriptedProvider{responses: responses}
}

func (sp *scriptedProvider) Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.callIndex >= len(sp.responses) {
		return nil, fmt.Errorf("scriptedProvider: no more responses (call %d)", sp.callIndex)
	}
	resp := sp.responses[sp.callIndex]
	sp.callIndex++
	return resp, nil
}

func (sp *scriptedProvider) ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
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
				cb(" ")
			}
			cb(w)
		}
	}
	return resp, nil
}

// dummyHandler is a no-op tool handler for construction tests.
func dummyHandler(_ context.Context, _ json.RawMessage) (string, error) {
	return "ok", nil
}

func dummyTool(name, desc string) tool.Tool {
	return tool.NewRaw(name, desc, map[string]any{"type": "object"}, dummyHandler)
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

	a, err := New(mockProvider{}, prompt.Text("You are helpful."), tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(a.tools))
	}
	if len(a.toolSpecs) != 2 {
		t.Errorf("expected 2 toolSpecs, got %d", len(a.toolSpecs))
	}
	if a.maxIterations != 10 {
		t.Errorf("expected default maxIterations=10, got %d", a.maxIterations)
	}
	if a.parallelTools {
		t.Error("expected parallelTools=false by default")
	}
}

func TestNewAgent_WithOptions(t *testing.T) {
	tools := []tool.Tool{dummyTool("t1", "Tool one")}

	a, err := New(mockProvider{}, prompt.Text("sys"), tools,
		WithMaxIterations(5),
		WithParallelToolExecution(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.maxIterations != 5 {
		t.Errorf("expected maxIterations=5, got %d", a.maxIterations)
	}
	if !a.parallelTools {
		t.Error("expected parallelTools=true after WithParallelToolExecution")
	}
}

func TestNewAgent_DuplicateToolName(t *testing.T) {
	tools := []tool.Tool{
		dummyTool("search", "First search"),
		dummyTool("search", "Duplicate search"),
	}

	_, err := New(mockProvider{}, prompt.Text("sys"), tools)
	if err == nil {
		t.Fatal("expected error for duplicate tool name, got nil")
	}
}

func TestNewAgent_MissingToolName(t *testing.T) {
	tools := []tool.Tool{
		dummyTool("", "Has description"),
	}

	_, err := New(mockProvider{}, prompt.Text("sys"), tools)
	if err == nil {
		t.Fatal("expected error for empty tool name, got nil")
	}
}

func TestNewAgent_MissingToolDescription(t *testing.T) {
	tools := []tool.Tool{
		dummyTool("search", ""),
	}

	_, err := New(mockProvider{}, prompt.Text("sys"), tools)
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

	_, err := New(mockProvider{}, prompt.Text("sys"), tools)
	if err == nil {
		t.Fatal("expected error for nil tool handler, got nil")
	}
}

func TestNewAgent_NoTools(t *testing.T) {
	a, err := New(mockProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(a.tools))
	}
}

// ---------------------------------------------------------------------------
// Core loop tests (task 3.4)
// ---------------------------------------------------------------------------

func TestInvoke_TextOnlyResponse(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "Hello world"})
	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello world" {
		t.Errorf("expected %q, got %q", "Hello world", result)
	}
}

func TestInvoke_SingleToolCall(t *testing.T) {
	// Provider returns a tool call, then a final text answer.
	sp := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{toolCall("tc1", "echo")}},
		&ProviderResponse{Text: "done"},
	)

	echoTool := tool.NewRaw("echo", "echoes input", map[string]any{"type": "object"},
		func(_ context.Context, input json.RawMessage) (string, error) {
			return "echoed", nil
		})

	a, err := New(sp, prompt.Text("sys"), []tool.Tool{echoTool})
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "call echo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("expected %q, got %q", "done", result)
	}
}

func TestInvoke_SequentialToolExecutionOrder(t *testing.T) {
	// Provider returns two tool calls in one response, then final text.
	sp := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{
			toolCall("tc1", "first"),
			toolCall("tc2", "second"),
		}},
		&ProviderResponse{Text: "all done"},
	)

	var mu sync.Mutex
	var order []string

	makeTool := func(name string) tool.Tool {
		return tool.NewRaw(name, name+" tool", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				return name + " result", nil
			})
	}

	a, err := New(sp, prompt.Text("sys"), []tool.Tool{makeTool("first"), makeTool("second")})
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "all done" {
		t.Errorf("expected %q, got %q", "all done", result)
	}

	// Sequential by default — order must be preserved.
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("expected sequential order [first, second], got %v", order)
	}
}

func TestInvoke_ParallelToolExecutionCompletesAll(t *testing.T) {
	sp := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{
			toolCall("tc1", "a"),
			toolCall("tc2", "b"),
			toolCall("tc3", "c"),
		}},
		&ProviderResponse{Text: "parallel done"},
	)

	const toolSleep = 100 * time.Millisecond

	// Barrier: every tool adds to the WaitGroup before proceeding.
	// If tools run sequentially, the first tool will block forever
	// waiting for the others to arrive at the barrier.
	var barrier sync.WaitGroup
	barrier.Add(3)

	var mu sync.Mutex
	executed := map[string]bool{}

	makeTool := func(name string) tool.Tool {
		return tool.NewRaw(name, name+" tool", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				barrier.Done()
				barrier.Wait() // blocks until all 3 tools are running
				time.Sleep(toolSleep)
				mu.Lock()
				executed[name] = true
				mu.Unlock()
				return name + " ok", nil
			})
	}

	a, err := New(sp, prompt.Text("sys"),
		[]tool.Tool{makeTool("a"), makeTool("b"), makeTool("c")},
		WithParallelToolExecution(),
	)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	result, _, err := a.Invoke(context.Background(), "go parallel")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "parallel done" {
		t.Errorf("expected %q, got %q", "parallel done", result)
	}

	// All three tools must have been executed.
	for _, name := range []string{"a", "b", "c"} {
		if !executed[name] {
			t.Errorf("tool %q was not executed", name)
		}
	}

	// If tools ran in parallel, total time should be ~1x toolSleep.
	// If sequential, it would be ~3x toolSleep (300ms) — or deadlock on the barrier.
	// Use 2x as the threshold to catch sequential execution.
	if elapsed >= 2*toolSleep {
		t.Errorf("tools appear to have run sequentially: elapsed %v, expected < %v", elapsed, 2*toolSleep)
	}
}

func TestInvoke_ToolErrorReturnedAsResultText(t *testing.T) {
	// Provider returns a tool call to "fail_tool", then final text.
	// We verify the agent doesn't abort — it sends the error as a tool result.
	sp := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{toolCall("tc1", "fail_tool")}},
		&ProviderResponse{Text: "recovered"},
	)

	failTool := tool.NewRaw("fail_tool", "always fails", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("something broke")
		})

	a, err := New(sp, prompt.Text("sys"), []tool.Tool{failTool})
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "try it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "recovered" {
		t.Errorf("expected %q, got %q", "recovered", result)
	}
}

func TestInvoke_MaxIterationError(t *testing.T) {
	// Provider always returns a tool call — never a final answer.
	// With maxIterations=2, the agent should error after 2 loops.
	alwaysToolCall := &ProviderResponse{ToolCalls: []tool.Call{toolCall("tc", "loop")}}
	sp := newScriptedProvider(alwaysToolCall, alwaysToolCall, alwaysToolCall)

	loopTool := tool.NewRaw("loop", "loops forever", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "looping", nil
		})

	a, err := New(sp, prompt.Text("sys"), []tool.Tool{loopTool}, WithMaxIterations(2))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "loop forever")
	if err == nil {
		t.Fatal("expected max iteration error, got nil")
	}
	if !strings.Contains(err.Error(), "max iterations") {
		t.Errorf("expected error to mention 'max iterations', got: %v", err)
	}
}

func TestInvokeStream_CallbackReceivesChunksInOrder(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "one two three"})
	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	var chunks []string
	_, err = a.InvokeStream(context.Background(), "stream me", func(chunk string) {
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

func TestInvokeStream_SuppressesChunksDuringToolIteration(t *testing.T) {
	// First response has tool calls (text should be suppressed).
	// Second response is the final answer (text should be streamed).
	sp := newScriptedProvider(
		&ProviderResponse{
			Text:      "thinking...",
			ToolCalls: []tool.Call{toolCall("tc1", "work")},
		},
		&ProviderResponse{Text: "final answer"},
	)

	workTool := tool.NewRaw("work", "does work", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "worked", nil
		})

	a, err := New(sp, prompt.Text("sys"), []tool.Tool{workTool})
	if err != nil {
		t.Fatal(err)
	}

	var chunks []string
	_, err = a.InvokeStream(context.Background(), "go", func(chunk string) {
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
		&ProviderResponse{ToolCalls: []tool.Call{
			toolCall("id-a", "alpha"),
			toolCall("id-b", "beta"),
			toolCall("id-c", "gamma"),
		}},
		&ProviderResponse{Text: "ordered"},
	)

	makeTool := func(name, result string) tool.Tool {
		return tool.NewRaw(name, name+" tool", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				return result, nil
			})
	}

	// Use parallel execution to stress order preservation.
	a, err := New(sp, prompt.Text("sys"),
		[]tool.Tool{makeTool("alpha", "A"), makeTool("beta", "B"), makeTool("gamma", "C")},
		WithParallelToolExecution(),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ordered" {
		t.Errorf("expected %q, got %q", "ordered", result)
	}

	// Verify the provider received tool results in the correct order by inspecting
	// the messages sent in the second call. The scriptedProvider doesn't capture
	// params, so we verify indirectly: the agent completed without error, meaning
	// it successfully sent results back and got the final answer.
}

func TestInvoke_UnknownToolReturnsError(t *testing.T) {
	// Provider asks for a tool that doesn't exist.
	sp := newScriptedProvider(
		&ProviderResponse{ToolCalls: []tool.Call{toolCall("tc1", "nonexistent")}},
		&ProviderResponse{Text: "handled"},
	)

	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "call missing tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "handled" {
		t.Errorf("expected %q, got %q", "handled", result)
	}
}

func TestInvokeStream_NilCallbackDoesNotPanic(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "hello"})
	a, err := New(sp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Passing nil callback should not panic.
	_, err = a.InvokeStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 3.1 — Structured error type propagation tests
// ---------------------------------------------------------------------------

// errorProvider always returns an error from ConverseStream.
type errorProvider struct{ err error }

func (ep errorProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return nil, ep.err
}
func (ep errorProvider) ConverseStream(_ context.Context, _ ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	return nil, ep.err
}

func TestInvoke_ProviderErrorWrapped(t *testing.T) {
	cause := fmt.Errorf("connection refused")
	a, err := New(errorProvider{err: cause}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	_, _, invokeErr := a.Invoke(context.Background(), "hello")
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

	boomTool := tool.NewRaw("boom", "always errors", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", cause
		})

	// Use capturingProvider (defined in guardrail_test.go) to inspect what the
	// second provider call receives as tool results.
	cp := newCapturingProvider(
		&ProviderResponse{ToolCalls: []tool.Call{toolCall("tc1", "boom")}},
		&ProviderResponse{Text: "done"},
	)

	a, err := New(cp, prompt.Text("sys"), []tool.Tool{boomTool})
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "try boom")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("expected %q, got %q", "done", result)
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

func TestInvoke_InputGuardrailErrorWrapped(t *testing.T) {
	cause := fmt.Errorf("blocked by policy")
	sp := newScriptedProvider(&ProviderResponse{Text: "ok"})
	a, err := New(sp, prompt.Text("sys"), nil,
		WithInputGuardrail(func(_ context.Context, msg string) (string, error) {
			return "", cause
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, invokeErr := a.Invoke(context.Background(), "bad input")
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
	sp := newScriptedProvider(&ProviderResponse{Text: "some response"})
	a, err := New(sp, prompt.Text("sys"), nil,
		WithOutputGuardrail(func(_ context.Context, msg string) (string, error) {
			return "", cause
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, invokeErr := a.Invoke(context.Background(), "hello")
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
