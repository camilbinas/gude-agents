package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// WithProviderTimeout tests
// ---------------------------------------------------------------------------

// slowProvider blocks for the given duration before responding.
type slowProvider struct {
	delay    time.Duration
	response *ModelResponse
}

func (p *slowProvider) Name() string { return "mock" }

func (p *slowProvider) Stream(ctx context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	select {
	case <-time.After(p.delay):
		if emit != nil && p.response.Text != "" {
			emit(ModelEvent{Type: ModelEventText, Text: p.response.Text})
		}
		return p.response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestWithProviderTimeout_ProviderRespondsInTime(t *testing.T) {
	sp := &slowProvider{
		delay:    10 * time.Millisecond,
		response: &ModelResponse{Text: "fast"},
	}
	a, err := New(sp, "sys", WithProviderTimeout(1*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "hi")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if result.Text != "fast" {
		t.Errorf("expected %q, got %q", "fast", result.Text)
	}
}

func TestWithProviderTimeout_ProviderTimesOut(t *testing.T) {
	sp := &slowProvider{
		delay:    5 * time.Second,
		response: &ModelResponse{Text: "slow"},
	}
	a, err := New(sp, "sys", WithProviderTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hi")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	// Should be a ProviderError wrapping context.DeadlineExceeded.
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ProviderError, got %T: %v", err, err)
	}
	if !errors.Is(pe.Cause, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded cause, got: %v", pe.Cause)
	}
}

func TestWithProviderTimeout_ZeroMeansNoTimeout(t *testing.T) {
	sp := &slowProvider{
		delay:    10 * time.Millisecond,
		response: &ModelResponse{Text: "ok"},
	}
	a, err := New(sp, "sys", WithProviderTimeout(0))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "hi")
	if err != nil {
		t.Fatalf("expected success with zero timeout, got: %v", err)
	}
	if result.Text != "ok" {
		t.Errorf("expected %q, got %q", "ok", result.Text)
	}
}

func TestWithProviderTimeout_NegativeReturnsError(t *testing.T) {
	_, err := New(mockProvider{}, "sys", WithProviderTimeout(-1*time.Second))
	if err == nil {
		t.Fatal("expected error for negative timeout")
	}
}

// ---------------------------------------------------------------------------
// No framework retry
// ---------------------------------------------------------------------------

// failNProvider fails the first N calls, then succeeds.
type failNProvider struct {
	failCount int
	calls     atomic.Int32
	response  *ModelResponse
}

func (p *failNProvider) Name() string { return "mock" }
func (p *failNProvider) Stream(_ context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	n := int(p.calls.Add(1))
	if n <= p.failCount {
		return nil, fmt.Errorf("transient error (call %d)", n)
	}
	if emit != nil && p.response.Text != "" {
		emit(ModelEvent{Type: ModelEventText, Text: p.response.Text})
	}
	return p.response, nil
}

func TestProviderFailureIsNotRetried(t *testing.T) {
	fp := &failNProvider{failCount: 1, response: &ModelResponse{Text: "ok"}}
	a, err := New(fp, "sys")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(Background(), "hi")
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("Invoke error = %v, want *ProviderError", err)
	}
	if calls := int(fp.calls.Load()); calls != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 (the agent loop never retries)", calls)
	}
}

func TestProviderTimeoutIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	sp := &funcProvider{
		fn: func(ctx context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
			calls.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	a, err := New(sp, "sys", WithProviderTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(Background(), "hi")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Invoke error = %v, want deadline exceeded", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1", got)
	}
}

func TestProviderStreamFailureAfterVisibleOutputIsFinal(t *testing.T) {
	partialErr := errors.New("stream interrupted after partial output")
	calls := 0
	provider := &funcProvider{fn: func(_ context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
		calls++
		if calls == 1 {
			emit(ModelEvent{Type: ModelEventText, Text: "partial"})
			return nil, partialErr
		}
		return &ModelResponse{Text: "replacement"}, nil
	}}
	a, err := New(provider, "sys")
	if err != nil {
		t.Fatal(err)
	}

	var chunks []string
	for chunk, serr := range a.TextStream(Background(), "hi") {
		if serr != nil {
			err = serr
			break
		}
		chunks = append(chunks, chunk)
	}
	if !errors.Is(err, partialErr) {
		t.Fatalf("TextStream error = %v, want partial stream error", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1 after visible output", calls)
	}
	if len(chunks) != 1 || chunks[0] != "partial" {
		t.Fatalf("visible chunks = %#v, want only partial", chunks)
	}
}

func TestProviderStreamFailureAfterThinkingIsFinal(t *testing.T) {
	thinkingErr := errors.New("stream interrupted after thinking")
	calls := 0
	provider := &funcProvider{fn: func(_ context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
		calls++
		emit(ModelEvent{Type: ModelEventThinking, Text: "reasoning"})
		return nil, thinkingErr
	}}
	a, err := New(provider, "sys")
	if err != nil {
		t.Fatal(err)
	}

	events, streamErr := collectStream(a.Stream(Background(), "hi"))
	if !errors.Is(streamErr, thinkingErr) {
		t.Fatalf("Stream error = %v, want thinking stream error", streamErr)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1 after thinking event", calls)
	}
	if len(events) < 2 || events[1].Type != EventThinking || events[1].Thinking == nil || events[1].Thinking.Content != "reasoning" {
		t.Fatalf("events = %#v, want reasoning thinking event", events)
	}
}

// unknownBlock is a custom ContentBlock implementation that is not one of the
// registered block types. It is used to verify that provider switch statements
// have a safe default and do not panic on unknown types.
type unknownBlock struct{}

func (unknownBlock) contentBlock() {}

// TestKnownContentBlocks_AllImplementInterface verifies that all known ContentBlock
// types satisfy the sealed interface at compile time and runtime.
//
// Requirements: 1.1
func TestKnownContentBlocks_AllImplementInterface(t *testing.T) {
	var _ ContentBlock = TextBlock{Text: "hello"}
	var _ ContentBlock = ToolUseBlock{ToolUseID: "id", Name: "tool"}
	var _ ContentBlock = ToolResultBlock{ToolUseID: "id", Content: "result"}
	// If this compiles and runs, all known ContentBlock types satisfy the interface.
}

// Feature: prompt-caching-support, Property 6: TokenUsage.Total() excludes cache tokens

// TestProperty_TokenUsageTotalExcludesCacheTokens verifies that for any TokenUsage
// value with arbitrary InputTokens, OutputTokens, CacheReadTokens, and
// CacheWriteTokens, Total() returns exactly InputTokens + OutputTokens and never
// includes cache tokens in the sum.
//
// **Validates: Requirements 5.9, 8.1**
func TestProperty_TokenUsageTotalExcludesCacheTokens(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate arbitrary token counts including negative values to stress
		// the invariant across the full int range.
		inputTokens := rapid.Int().Draw(rt, "inputTokens")
		outputTokens := rapid.Int().Draw(rt, "outputTokens")
		cacheReadTokens := rapid.Int().Draw(rt, "cacheReadTokens")
		cacheWriteTokens := rapid.Int().Draw(rt, "cacheWriteTokens")

		u := TokenUsage{
			InputTokens:      inputTokens,
			OutputTokens:     outputTokens,
			CacheReadTokens:  cacheReadTokens,
			CacheWriteTokens: cacheWriteTokens,
		}

		got := u.Total()
		want := inputTokens + outputTokens

		if got != want {
			rt.Fatalf(
				"Total() = %d, want %d (InputTokens=%d, OutputTokens=%d, CacheReadTokens=%d, CacheWriteTokens=%d)",
				got, want, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens,
			)
		}
	})
}

type capabilityTestProvider struct {
	caps ModelCapabilities
}

func (capabilityTestProvider) Name() string { return "capability-test" }
func (capabilityTestProvider) Stream(context.Context, ModelRequest, func(ModelEvent)) (*ModelResponse, error) {
	return &ModelResponse{}, nil
}
func (p capabilityTestProvider) Capabilities() ModelCapabilities { return p.caps }

type plainProvider struct{}

func (plainProvider) Name() string { return "plain" }
func (plainProvider) Stream(context.Context, ModelRequest, func(ModelEvent)) (*ModelResponse, error) {
	return &ModelResponse{}, nil
}

func TestCapabilitiesOf(t *testing.T) {
	want := ModelCapabilities{
		ContextWindowTokens:    200000,
		MaxOutputTokens:        16000,
		ToolUse:                Supported,
		NativeStructuredOutput: Unsupported,
		ToolChoice:             ToolChoiceCapabilities{Auto: Supported, Required: Supported, Specific: Unsupported},
	}

	if got := CapabilitiesOf(capabilityTestProvider{caps: want}); got != want {
		t.Fatalf("CapabilitiesOf(capability provider) = %#v, want %#v", got, want)
	}
	if got := CapabilitiesOf(plainProvider{}); got != (ModelCapabilities{}) {
		t.Fatalf("CapabilitiesOf(plain provider) = %#v, want unknown zero value", got)
	}
	if got := CapabilitiesOf(nil); got != (ModelCapabilities{}) {
		t.Fatalf("CapabilitiesOf(nil) = %#v, want unknown zero value", got)
	}
}

func TestMergeModelCapabilitiesPreservesUnspecifiedFields(t *testing.T) {
	base := ModelCapabilities{
		ContextWindowTokens:    200000,
		MaxOutputTokens:        16000,
		ToolUse:                Supported,
		NativeStructuredOutput: Unsupported,
		ToolChoice:             ToolChoiceCapabilities{Auto: Supported, Required: Supported, Specific: Unsupported},
	}
	got := MergeModelCapabilities(base, ModelCapabilities{
		ToolChoice: ToolChoiceCapabilities{Specific: Supported},
	})
	want := base
	want.ToolChoice.Specific = Supported
	if got != want {
		t.Fatalf("MergeModelCapabilities() = %#v, want %#v", got, want)
	}
}

var _ Provider = plainProvider{}
var _ Provider = capabilityTestProvider{}
var _ CapabilityProvider = capabilityTestProvider{}
