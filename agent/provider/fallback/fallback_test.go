package fallback_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/fallback"
	"github.com/camilbinas/gude-agents/agent/structured"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubProvider struct {
	err      error
	resp     *agent.ModelResponse
	events   []agent.ModelEvent
	calls    int
	requests []agent.ModelRequest
}

func (s *stubProvider) Name() string { return "mock" }

func (s *stubProvider) Stream(_ context.Context, req agent.ModelRequest, emit func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	s.calls++
	s.requests = append(s.requests, req)
	for _, event := range s.events {
		if emit != nil {
			emit(event)
		}
	}
	return s.resp, s.err
}

func okProvider(text string) *stubProvider {
	return &stubProvider{
		resp: &agent.ModelResponse{
			Text:  text,
			Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
		events: []agent.ModelEvent{{Type: agent.ModelEventText, Text: text}},
	}
}

func failProvider(err error) *stubProvider {
	return &stubProvider{err: err}
}

func TestStream_PrimarySucceeds(t *testing.T) {
	backup := failProvider(errors.New("backup should not run"))
	p := fallback.New(okProvider("hello"), backup)

	resp, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)

	require.NoError(t, err)
	assert.Equal(t, "hello", resp.Text)
	assert.Equal(t, agent.TokenUsage{InputTokens: 10, OutputTokens: 5}, resp.Usage)
	assert.Zero(t, backup.calls)
}

func TestStream_FallsBackOnPreEmissionError(t *testing.T) {
	primaryErr := errors.New("primary unavailable")
	backup := okProvider("from backup")
	p := fallback.New(failProvider(primaryErr), backup)
	var events []agent.ModelEvent

	resp, err := p.Stream(context.Background(), agent.ModelRequest{}, func(event agent.ModelEvent) {
		events = append(events, event)
	})

	require.NoError(t, err)
	assert.Equal(t, "from backup", resp.Text)
	assert.Equal(t, []agent.ModelEvent{{Type: agent.ModelEventText, Text: "from backup"}}, events)
	assert.Equal(t, 1, backup.calls)
}

func TestStream_TriesAllProvidersInOrder(t *testing.T) {
	first := failProvider(errors.New("first failed"))
	second := failProvider(errors.New("second failed"))
	third := okProvider("third")
	request := agent.ModelRequest{System: "be concise"}
	p := fallback.New(first, second, third)

	resp, err := p.Stream(context.Background(), request, nil)

	require.NoError(t, err)
	assert.Equal(t, "third", resp.Text)
	assert.Equal(t, 1, first.calls)
	assert.Equal(t, 1, second.calls)
	assert.Equal(t, 1, third.calls)
	require.Len(t, third.requests, 1)
	assert.Equal(t, request, third.requests[0])
}

func TestStream_AllFail_ReturnsLastError(t *testing.T) {
	firstErr := errors.New("first failed")
	lastErr := errors.New("last failed")
	p := fallback.New(failProvider(firstErr), failProvider(lastErr))

	resp, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)

	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, lastErr)
	assert.Contains(t, err.Error(), "all providers failed")
	assert.Contains(t, err.Error(), "provider[1]")
}

func TestStream_ForwardsTypedEvents(t *testing.T) {
	primary := okProvider("complete")
	primary.events = []agent.ModelEvent{
		{Type: agent.ModelEventThinking, Text: "reasoning"},
		{Type: agent.ModelEventText, Text: "answer"},
	}
	p := fallback.New(primary)
	var events []agent.ModelEvent

	resp, err := p.Stream(context.Background(), agent.ModelRequest{}, func(event agent.ModelEvent) {
		events = append(events, event)
	})

	require.NoError(t, err)
	assert.Equal(t, "complete", resp.Text)
	assert.Equal(t, primary.events, events)
}

func TestStream_DoesNotReplayAfterEventEscapes(t *testing.T) {
	for _, eventType := range []agent.ModelEventType{agent.ModelEventText, agent.ModelEventThinking} {
		t.Run(string(eventType), func(t *testing.T) {
			streamErr := errors.New("stream interrupted")
			primary := failProvider(streamErr)
			primary.events = []agent.ModelEvent{{Type: eventType, Text: "escaped"}}
			backup := okProvider("must not replay")
			p := fallback.New(primary, backup)
			var events []agent.ModelEvent

			resp, err := p.Stream(context.Background(), agent.ModelRequest{}, func(event agent.ModelEvent) {
				events = append(events, event)
			})

			assert.Nil(t, resp)
			require.Error(t, err)
			assert.ErrorIs(t, err, streamErr)
			assert.NotContains(t, err.Error(), "all providers failed")
			assert.Equal(t, primary.events, events)
			assert.Zero(t, backup.calls)
		})
	}
}

func TestStream_NilEmitterCanFallBackAfterProviderProducesEvents(t *testing.T) {
	primary := failProvider(errors.New("stream interrupted"))
	primary.events = []agent.ModelEvent{{Type: agent.ModelEventText, Text: "not exposed"}}
	backup := okProvider("backup")
	p := fallback.New(primary, backup)

	resp, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)

	require.NoError(t, err)
	assert.Equal(t, "backup", resp.Text)
	assert.Equal(t, 1, backup.calls)
}

func TestStream_SingleProvider(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		p := fallback.New(okProvider("only one"))

		resp, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)

		require.NoError(t, err)
		assert.Equal(t, "only one", resp.Text)
	})

	t.Run("failure", func(t *testing.T) {
		providerErr := errors.New("service unavailable")
		p := fallback.New(failProvider(providerErr))

		resp, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)

		assert.Nil(t, resp)
		require.Error(t, err)
		assert.ErrorIs(t, err, providerErr)
	})
}

type capabilityStubProvider struct {
	*stubProvider
	caps agent.ModelCapabilities
}

func (s *capabilityStubProvider) Capabilities() agent.ModelCapabilities { return s.caps }

func TestCapabilitiesConservativelyAggregateFallbackChain(t *testing.T) {
	first := &capabilityStubProvider{
		stubProvider: okProvider("first"),
		caps: agent.ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        16000,
			ToolUse:                agent.Supported,
			NativeStructuredOutput: agent.Unsupported,
			ToolChoice:             agent.ToolChoiceCapabilities{Auto: agent.Supported, Required: agent.Supported, Specific: agent.Supported},
		},
	}
	second := &capabilityStubProvider{
		stubProvider: okProvider("second"),
		caps: agent.ModelCapabilities{
			ContextWindowTokens:    128000,
			MaxOutputTokens:        8000,
			ToolUse:                agent.Supported,
			NativeStructuredOutput: agent.Unsupported,
			ToolChoice:             agent.ToolChoiceCapabilities{Auto: agent.Supported, Required: agent.Unsupported, Specific: agent.Supported},
		},
	}

	got := fallback.New(first, second).Capabilities()
	want := agent.ModelCapabilities{
		ContextWindowTokens:    128000,
		MaxOutputTokens:        8000,
		ToolUse:                agent.Supported,
		NativeStructuredOutput: agent.Unsupported,
		ToolChoice: agent.ToolChoiceCapabilities{
			Auto:     agent.Supported,
			Required: agent.Unsupported,
			Specific: agent.Supported,
		},
	}
	assert.Equal(t, want, got)
}

func TestCapabilitiesReturnsUnknownWhenFallbackDelegateDoesNotReportMetadata(t *testing.T) {
	known := &capabilityStubProvider{
		stubProvider: okProvider("known"),
		caps: agent.ModelCapabilities{
			ContextWindowTokens: 200000,
			MaxOutputTokens:     16000,
			ToolUse:             agent.Supported,
			ToolChoice:          agent.ToolChoiceCapabilities{Specific: agent.Supported},
		},
	}

	got := fallback.New(known, okProvider("legacy")).Capabilities()
	assert.Equal(t, agent.ModelCapabilities{}, got)
}

func TestCapabilitiesKnownUnsupportedWinsOverSupported(t *testing.T) {
	primary := &capabilityStubProvider{
		stubProvider: okProvider("primary"),
		caps: agent.ModelCapabilities{
			ToolUse: agent.Supported,
			ToolChoice: agent.ToolChoiceCapabilities{
				Auto:     agent.Supported,
				Specific: agent.Supported,
			},
		},
	}
	fallbackProvider := &capabilityStubProvider{
		stubProvider: okProvider("fallback"),
		caps: agent.ModelCapabilities{
			ToolUse: agent.Supported,
			ToolChoice: agent.ToolChoiceCapabilities{
				Auto:     agent.Supported,
				Specific: agent.Unsupported,
			},
		},
	}

	got := fallback.New(primary, fallbackProvider).Capabilities()
	assert.Equal(t, agent.Supported, got.ToolUse)
	assert.Equal(t, agent.Supported, got.ToolChoice.Auto)
	assert.Equal(t, agent.Unsupported, got.ToolChoice.Specific)
}

type structuredProfile struct {
	Name  string `json:"name" description:"A name" required:"true"`
	Count int    `json:"count" description:"A count"`
}

func structuredStub(raw string, specific agent.Capability) *capabilityStubProvider {
	return &capabilityStubProvider{
		stubProvider: &stubProvider{resp: &agent.ModelResponse{ToolCalls: []tool.Call{{
			ToolUseID: "structured-call",
			Name:      "structured_output",
			Input:     json.RawMessage(raw),
		}}}},
		caps: agent.ModelCapabilities{
			ToolUse:    agent.Supported,
			ToolChoice: agent.ToolChoiceCapabilities{Auto: agent.Supported, Specific: specific},
		},
	}
}

// A chain whose backup cannot force a specific tool must make structured output
// fall back to automatic tool choice, even when the primary could force one.
func TestStructuredInvokeUsesAutomaticChoiceWhenBackupCannotForceTool(t *testing.T) {
	primary := structuredStub(`{"name":"Ada","count":3}`, agent.Supported)
	backup := structuredStub(`{"name":"Grace","count":4}`, agent.Unsupported)
	a, err := agent.New(fallback.New(primary, backup), "sys")
	require.NoError(t, err)

	got, err := structured.Invoke[structuredProfile](agent.Background(), a, "input")

	require.NoError(t, err)
	assert.Equal(t, "Ada", got.Value.Name)
	assert.Equal(t, 1, primary.calls)
	assert.Zero(t, backup.calls)
	require.Len(t, primary.requests, 1)
	assert.Nil(t, primary.requests[0].ToolChoice, "want provider-default automatic choice")
}

// cancelProvider cancels the shared context and fails, like a provider whose
// stream was aborted by the caller.
type cancelProvider struct {
	cancel context.CancelFunc
	calls  int
}

func (c *cancelProvider) Name() string { return "cancel" }

func (c *cancelProvider) Stream(ctx context.Context, _ agent.ModelRequest, _ func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	c.calls++
	c.cancel()
	return nil, ctx.Err()
}

func TestStream_DoesNotFallBackAfterContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	primary := &cancelProvider{cancel: cancel}
	backup := okProvider("backup")

	_, err := fallback.New(primary, backup).Stream(ctx, agent.ModelRequest{}, nil)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, primary.calls)
	assert.Equal(t, 0, backup.calls, "backup must not run under a cancelled context")
}

func TestStream_DoesNotStartWithDoneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	primary := okProvider("primary")

	_, err := fallback.New(primary).Stream(ctx, agent.ModelRequest{}, nil)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, primary.calls)
}
