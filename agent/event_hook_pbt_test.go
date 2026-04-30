package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Feature: event-hook, Property 1: Nil-safe dispatch
// Validates: Requirements 3.2, 7.1
//
// For any sequence of dispatch operations with nil EventHook, no panics occur.
// ---------------------------------------------------------------------------

func TestProperty_NilSafeDispatch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		h := hooks{event: nil}
		ctx := context.Background()

		// Generate a random sequence of dispatch operations.
		numOps := rapid.IntRange(1, 20).Draw(t, "numOps")
		for i := range numOps {
			op := rapid.IntRange(0, 4).Draw(t, fmt.Sprintf("op_%d", i))
			switch op {
			case 0: // tool start + finish success
				name := rapid.StringMatching(`[a-z_]{1,20}`).Draw(t, fmt.Sprintf("toolName_%d", i))
				input := json.RawMessage(rapid.SampledFrom([]string{
					`{}`, `{"key":"value"}`, `{"a":1}`, `null`,
				}).Draw(t, fmt.Sprintf("input_%d", i)))
				_, tf := h.onToolStart(ctx, name, input)
				tf.finish(nil, rapid.String().Draw(t, fmt.Sprintf("output_%d", i)))
			case 1: // tool start + finish error
				name := rapid.StringMatching(`[a-z_]{1,20}`).Draw(t, fmt.Sprintf("toolNameErr_%d", i))
				_, tf := h.onToolStart(ctx, name, json.RawMessage(`{}`))
				tf.finish(fmt.Errorf("error_%d", i), "")
			case 2: // provider call start + finish success
				_, pf := h.onProviderCallStart(ctx, ProviderCallParams{}, "model-id")
				toolCount := rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("toolCount_%d", i))
				pf.finish(nil, TokenUsage{}, toolCount, "text")
			case 3: // provider call start + finish error
				_, pf := h.onProviderCallStart(ctx, ProviderCallParams{}, "model-id")
				pf.finish(fmt.Errorf("provider_error_%d", i), TokenUsage{}, 0, "")
			case 4: // invoke start + finish
				_, inv := h.onInvokeStart(ctx, InvokeSpanParams{})
				inv.finish(nil, TokenUsage{})
			}
		}
		// If we reach here without panicking, the property holds.
	})
}

// ---------------------------------------------------------------------------
// Feature: event-hook, Property 2: Tool call start dispatch
// Validates: Requirements 4.1
//
// For any tool name and JSON input, OnToolCallStart receives exact values.
// ---------------------------------------------------------------------------

func TestProperty_ToolCallStartDispatch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hook := &recordingEventHook{}
		h := hooks{event: hook}
		ctx := context.Background()

		toolName := rapid.StringMatching(`[a-zA-Z_][a-zA-Z0-9_]{0,30}`).Draw(t, "toolName")
		jsonInput := rapid.SampledFrom([]string{
			`{}`,
			`{"key":"value"}`,
			`{"num":42,"flag":true}`,
			`{"nested":{"a":"b"}}`,
			`{"arr":[1,2,3]}`,
		}).Draw(t, "jsonInput")
		input := json.RawMessage(jsonInput)

		_, tf := h.onToolStart(ctx, toolName, input)
		tf.finish(nil, "output")

		hook.mu.Lock()
		defer hook.mu.Unlock()

		if len(hook.toolStarts) != 1 {
			t.Fatalf("expected 1 tool start event, got %d", len(hook.toolStarts))
		}
		ev := hook.toolStarts[0]
		if ev.toolName != toolName {
			t.Fatalf("tool name mismatch: expected %q, got %q", toolName, ev.toolName)
		}
		if string(ev.input) != string(input) {
			t.Fatalf("input mismatch: expected %s, got %s", string(input), string(ev.input))
		}
	})
}

// ---------------------------------------------------------------------------
// Feature: event-hook, Property 3: Tool call end dispatch
// Validates: Requirements 4.2, 4.3
//
// For any tool result (success or failure), OnToolCallEnd receives correct
// output/error/duration.
// ---------------------------------------------------------------------------

func TestProperty_ToolCallEndDispatch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hook := &recordingEventHook{}
		h := hooks{event: hook}
		ctx := context.Background()

		toolName := rapid.StringMatching(`[a-zA-Z_][a-zA-Z0-9_]{0,30}`).Draw(t, "toolName")
		isSuccess := rapid.Bool().Draw(t, "isSuccess")

		_, tf := h.onToolStart(ctx, toolName, json.RawMessage(`{}`))
		// Introduce a small sleep to ensure positive duration.
		time.Sleep(1 * time.Millisecond)

		var expectedOutput string
		var expectedErr error

		if isSuccess {
			expectedOutput = rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "output")
			tf.finish(nil, expectedOutput)
		} else {
			errMsg := rapid.StringMatching(`[a-zA-Z0-9 ]{1,30}`).Draw(t, "errMsg")
			expectedErr = fmt.Errorf("%s", errMsg)
			tf.finish(expectedErr, "")
		}

		hook.mu.Lock()
		defer hook.mu.Unlock()

		if len(hook.toolEnds) != 1 {
			t.Fatalf("expected 1 tool end event, got %d", len(hook.toolEnds))
		}
		ev := hook.toolEnds[0]

		if ev.toolName != toolName {
			t.Fatalf("tool name mismatch: expected %q, got %q", toolName, ev.toolName)
		}
		if ev.duration <= 0 {
			t.Fatalf("expected positive duration, got %v", ev.duration)
		}

		if isSuccess {
			if ev.output != expectedOutput {
				t.Fatalf("output mismatch: expected %q, got %q", expectedOutput, ev.output)
			}
			if ev.err != nil {
				t.Fatalf("expected nil error, got %v", ev.err)
			}
		} else {
			if ev.output != "" {
				t.Fatalf("expected empty output on error, got %q", ev.output)
			}
			if ev.err == nil {
				t.Fatalf("expected non-nil error, got nil")
			}
			if ev.err.Error() != expectedErr.Error() {
				t.Fatalf("error mismatch: expected %q, got %q", expectedErr.Error(), ev.err.Error())
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Feature: event-hook, Property 4: Stop reason derivation
// Validates: Requirements 5.2, 5.3, 8.1, 8.2, 8.3
//
// For any provider response shape, correct stop reason is derived.
// ---------------------------------------------------------------------------

func TestProperty_StopReasonDerivation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		toolCallCount := rapid.IntRange(0, 100).Draw(t, "toolCallCount")
		hasError := rapid.Bool().Draw(t, "hasError")

		var err error
		if hasError {
			err = fmt.Errorf("error_%d", rapid.IntRange(1, 1000).Draw(t, "errCode"))
		}

		got := deriveStopReason(toolCallCount, err)

		var expected string
		if err != nil {
			expected = StopReasonError
		} else if toolCallCount > 0 {
			expected = StopReasonToolUse
		} else {
			expected = StopReasonEndTurn
		}

		if got != expected {
			t.Fatalf("deriveStopReason(toolCallCount=%d, err=%v) = %q, want %q",
				toolCallCount, err, got, expected)
		}
	})
}

// ---------------------------------------------------------------------------
// Feature: event-hook, Property 5: Model start ordering
// Validates: Requirements 5.1
//
// For any invocation, OnModelStart is called before provider.
// ---------------------------------------------------------------------------

// orderTrackingProvider records the order of OnModelStart vs provider call.
type orderTrackingProvider struct {
	mu       sync.Mutex
	sequence []string
	response *ProviderResponse
}

func (p *orderTrackingProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	p.mu.Lock()
	p.sequence = append(p.sequence, "provider")
	p.mu.Unlock()
	return p.response, nil
}

func (p *orderTrackingProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	p.mu.Lock()
	p.sequence = append(p.sequence, "provider")
	p.mu.Unlock()
	if cb != nil && p.response.Text != "" {
		cb(p.response.Text)
	}
	return p.response, nil
}

// orderTrackingEventHook records when OnModelStart is called.
type orderTrackingEventHook struct {
	mu       sync.Mutex
	sequence *[]string
}

func (h *orderTrackingEventHook) OnToolCallStart(_ context.Context, _ string, _ json.RawMessage) {}
func (h *orderTrackingEventHook) OnToolCallEnd(_ context.Context, _ string, _ string, _ error, _ time.Duration) {
}
func (h *orderTrackingEventHook) OnThinking(_ context.Context, _ string) {}
func (h *orderTrackingEventHook) OnModelStart(_ context.Context) {
	h.mu.Lock()
	*h.sequence = append(*h.sequence, "modelStart")
	h.mu.Unlock()
}
func (h *orderTrackingEventHook) OnModelEnd(_ context.Context, _ string) {}

func TestProperty_ModelStartOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate an arbitrary response text to vary the invocation.
		responseText := rapid.StringMatching(`[a-zA-Z0-9 ]{1,30}`).Draw(t, "responseText")

		var sequence []string
		provider := &orderTrackingProvider{
			sequence: sequence,
			response: &ProviderResponse{Text: responseText},
		}
		hook := &orderTrackingEventHook{sequence: &provider.sequence}

		a, err := New(provider, prompt.Text("sys"), nil)
		if err != nil {
			t.Fatalf("failed to create agent: %v", err)
		}

		ctx := WithEventHook(context.Background(), hook)
		_, _, err = a.Invoke(ctx, "hello")
		if err != nil {
			t.Fatalf("invoke failed: %v", err)
		}

		provider.mu.Lock()
		seq := make([]string, len(provider.sequence))
		copy(seq, provider.sequence)
		provider.mu.Unlock()

		// Verify OnModelStart appears before provider call.
		modelStartIdx := -1
		providerIdx := -1
		for i, s := range seq {
			if s == "modelStart" && modelStartIdx == -1 {
				modelStartIdx = i
			}
			if s == "provider" && providerIdx == -1 {
				providerIdx = i
			}
		}

		if modelStartIdx == -1 {
			t.Fatalf("OnModelStart was never called; sequence: %v", seq)
		}
		if providerIdx == -1 {
			t.Fatalf("provider was never called; sequence: %v", seq)
		}
		if modelStartIdx >= providerIdx {
			t.Fatalf("OnModelStart (idx=%d) was not called before provider (idx=%d); sequence: %v",
				modelStartIdx, providerIdx, seq)
		}
	})
}

// ---------------------------------------------------------------------------
// Feature: event-hook, Property 6: Thinking chunk forwarding
// Validates: Requirements 6.1
//
// For any chunk string, EventHook.OnThinking receives the exact chunk.
// ---------------------------------------------------------------------------

func TestProperty_ThinkingChunkForwarding(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		chunk := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,100}`).Draw(t, "chunk")

		hook := &recordingEventHook{}
		provider := &thinkingProvider{
			chunks:   []string{chunk},
			response: &ProviderResponse{Text: "done"},
		}

		a, err := New(provider, prompt.Text("sys"), nil)
		if err != nil {
			t.Fatalf("failed to create agent: %v", err)
		}

		ctx := WithEventHook(context.Background(), hook)
		_, _, err = a.Invoke(ctx, "think")
		if err != nil {
			t.Fatalf("invoke failed: %v", err)
		}

		// Verify EventHook received the chunk.
		hook.mu.Lock()
		hookChunks := make([]thinkingEvent, len(hook.thinkings))
		copy(hookChunks, hook.thinkings)
		hook.mu.Unlock()

		if len(hookChunks) != 1 {
			t.Fatalf("expected EventHook to receive 1 thinking chunk, got %d", len(hookChunks))
		}
		if hookChunks[0].chunk != chunk {
			t.Fatalf("EventHook chunk mismatch: expected %q, got %q", chunk, hookChunks[0].chunk)
		}
	})
}
