package graph

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// TestAgentNode_HookComposition validates Req 3.4:
// WHEN an agent already has its own hooks configured before being added as an Agent_Node,
// THE Agent_Node SHALL compose the agent's existing hooks with the graph-inherited hooks,
// calling both.
func TestAgentNode_HookComposition(t *testing.T) {
	t.Run("tracing hook composition calls both inner and graph hooks", func(t *testing.T) {
		graphHook := &recordingGraphTracingHook{}
		innerHook := &recordingAgentTracingHook{}

		bridge := newBridgeTracingHook(graphHook, "test-node", innerHook)
		if bridge == nil {
			t.Fatal("bridge should not be nil")
		}

		// Call OnInvokeStart — both hooks should be called.
		ctx := context.Background()
		ctx, finish := bridge.OnInvokeStart(ctx, agent.InvokeSpanParams{AgentName: "test"})
		if ctx == nil {
			t.Fatal("expected non-nil context")
		}

		// Verify inner hook was called.
		if innerHook.invokeStartCalls != 1 {
			t.Errorf("expected inner OnInvokeStart called 1 time, got %d", innerHook.invokeStartCalls)
		}

		// Verify graph hook was called (OnNodeStart).
		if graphHook.nodeStartCalls != 1 {
			t.Errorf("expected graph OnNodeStart called 1 time, got %d", graphHook.nodeStartCalls)
		}

		// Call finish — both should be called.
		finish(nil, agent.TokenUsage{InputTokens: 10}, "response")

		if innerHook.invokeEndCalls != 1 {
			t.Errorf("expected inner finish called 1 time, got %d", innerHook.invokeEndCalls)
		}
		if graphHook.nodeEndCalls != 1 {
			t.Errorf("expected graph finish called 1 time, got %d", graphHook.nodeEndCalls)
		}
	})

	t.Run("metrics hook composition calls inner hook only, not graph OnNodeStart", func(t *testing.T) {
		graphHook := &recordingGraphMetricsHook{}
		innerHook := &recordingAgentMetricsHook{}

		bridge := newBridgeMetricsHook(graphHook, "test-node", innerHook)
		if bridge == nil {
			t.Fatal("bridge should not be nil")
		}

		// Call OnInvokeStart — only inner hook should be called.
		// Node-level metrics (OnNodeStart) are tracked exclusively by the engine.
		finish := bridge.OnInvokeStart()

		if innerHook.invokeStartCalls != 1 {
			t.Errorf("expected inner OnInvokeStart called 1 time, got %d", innerHook.invokeStartCalls)
		}
		if graphHook.nodeStartCalls != 0 {
			t.Errorf("expected graph OnNodeStart NOT called from bridge (engine handles it), got %d", graphHook.nodeStartCalls)
		}

		// Call finish — only inner should be called.
		finish(nil, agent.TokenUsage{})

		if innerHook.invokeEndCalls != 1 {
			t.Errorf("expected inner finish called 1 time, got %d", innerHook.invokeEndCalls)
		}
		if graphHook.nodeEndCalls != 0 {
			t.Errorf("expected graph finish NOT called from bridge, got %d", graphHook.nodeEndCalls)
		}
	})

	t.Run("logging hook composition calls both inner and graph hooks", func(t *testing.T) {
		graphHook := &recordingLoggingHook{}
		innerHook := &recordingAgentLoggingHook{}

		bridge := newBridgeLoggingHook(graphHook, "test-node", innerHook)
		if bridge == nil {
			t.Fatal("bridge should not be nil")
		}

		// Call OnInvokeStart — both hooks should be called.
		bridge.OnInvokeStart(agent.InvokeSpanParams{})

		if innerHook.invokeStartCalls != 1 {
			t.Errorf("expected inner OnInvokeStart called 1 time, got %d", innerHook.invokeStartCalls)
		}
		if len(graphHook.nodeStartCalls) != 1 {
			t.Errorf("expected graph OnNodeStart called 1 time, got %d", len(graphHook.nodeStartCalls))
		}

		// Call OnInvokeEnd — both hooks should be called.
		bridge.OnInvokeEnd(nil, agent.TokenUsage{}, time.Millisecond)

		if innerHook.invokeEndCalls != 1 {
			t.Errorf("expected inner OnInvokeEnd called 1 time, got %d", innerHook.invokeEndCalls)
		}
		if len(graphHook.nodeEndCalls) != 1 {
			t.Errorf("expected graph OnNodeEnd called 1 time, got %d", len(graphHook.nodeEndCalls))
		}

		// Call OnToolStart — inner should be called.
		bridge.OnToolStart("my-tool")
		if innerHook.toolStartCalls != 1 {
			t.Errorf("expected inner OnToolStart called 1 time, got %d", innerHook.toolStartCalls)
		}

		// Call OnToolEnd — inner should be called.
		bridge.OnToolEnd("my-tool", nil, time.Millisecond)
		if innerHook.toolEndCalls != 1 {
			t.Errorf("expected inner OnToolEnd called 1 time, got %d", innerHook.toolEndCalls)
		}
	})
}

// TestAgentNode_NoGraphHooks_AgentHooksUnchanged validates Req 3.5:
// IF no observability hooks are configured on the Graph, THEN THE Agent_Node SHALL
// execute the agent with its own hooks unchanged.
func TestAgentNode_NoGraphHooks_AgentHooksUnchanged(t *testing.T) {
	t.Run("nil graph tracing hook returns nil bridge", func(t *testing.T) {
		innerHook := &recordingAgentTracingHook{}
		bridge := newBridgeTracingHook(nil, "test-node", innerHook)
		if bridge != nil {
			t.Error("expected nil bridge when graphHook is nil")
		}
	})

	t.Run("nil graph metrics hook returns nil bridge", func(t *testing.T) {
		innerHook := &recordingAgentMetricsHook{}
		bridge := newBridgeMetricsHook(nil, "test-node", innerHook)
		if bridge != nil {
			t.Error("expected nil bridge when graphHook is nil")
		}
	})

	t.Run("nil graph logging hook returns nil bridge", func(t *testing.T) {
		innerHook := &recordingAgentLoggingHook{}
		bridge := newBridgeLoggingHook(nil, "test-node", innerHook)
		if bridge != nil {
			t.Error("expected nil bridge when graphHook is nil")
		}
	})

	t.Run("agent hooks are preserved when no graph hooks configured", func(t *testing.T) {
		// When graph hooks are nil, the factory returns nil, meaning the agent's
		// own hooks should be used directly (unchanged).
		tracingBridge := newBridgeTracingHook(nil, "node", &recordingAgentTracingHook{})
		metricsBridge := newBridgeMetricsHook(nil, "node", &recordingAgentMetricsHook{})
		loggingBridge := newBridgeLoggingHook(nil, "node", &recordingAgentLoggingHook{})

		if tracingBridge != nil {
			t.Error("tracing bridge should be nil when no graph tracing hook")
		}
		if metricsBridge != nil {
			t.Error("metrics bridge should be nil when no graph metrics hook")
		}
		if loggingBridge != nil {
			t.Error("logging bridge should be nil when no graph logging hook")
		}
	})
}

// ── recording agent-level hook mocks ─────────────────────────────────────────

type recordingAgentTracingHook struct {
	invokeStartCalls int
	invokeEndCalls   int
}

func (h *recordingAgentTracingHook) OnInvokeStart(ctx context.Context, _ agent.InvokeSpanParams) (context.Context, func(error, agent.TokenUsage, string)) {
	h.invokeStartCalls++
	return ctx, func(_ error, _ agent.TokenUsage, _ string) {
		h.invokeEndCalls++
	}
}

func (h *recordingAgentTracingHook) OnIterationStart(ctx context.Context, _ int) (context.Context, func(int, bool)) {
	return ctx, func(_ int, _ bool) {}
}

func (h *recordingAgentTracingHook) OnProviderCallStart(ctx context.Context, _ agent.ProviderCallParams) (context.Context, func(error, agent.TokenUsage, int, string)) {
	return ctx, func(_ error, _ agent.TokenUsage, _ int, _ string) {}
}

func (h *recordingAgentTracingHook) OnToolStart(ctx context.Context, _ string, _ json.RawMessage) (context.Context, func(error, string)) {
	return ctx, func(_ error, _ string) {}
}

func (h *recordingAgentTracingHook) OnGuardrailStart(ctx context.Context, _ string, _ string) (context.Context, func(error, string)) {
	return ctx, func(_ error, _ string) {}
}

func (h *recordingAgentTracingHook) OnConversationStart(ctx context.Context, _ string, _ string) (context.Context, func(error)) {
	return ctx, func(_ error) {}
}

func (h *recordingAgentTracingHook) OnRetrieverStart(ctx context.Context, _ string) (context.Context, func(error, int)) {
	return ctx, func(_ error, _ int) {}
}

func (h *recordingAgentTracingHook) OnMaxIterationsExceeded(_ context.Context, _ int) {}

// ── recording graph tracing hook mock ────────────────────────────────────────

type recordingGraphTracingHook struct {
	graphRunStartCalls int
	nodeStartCalls     int
	nodeEndCalls       int
}

func (h *recordingGraphTracingHook) OnGraphRunStart(ctx context.Context) (context.Context, func(error, int)) {
	h.graphRunStartCalls++
	return ctx, func(_ error, _ int) {}
}

func (h *recordingGraphTracingHook) OnNodeStart(ctx context.Context, _ string) (context.Context, func(error)) {
	h.nodeStartCalls++
	return ctx, func(_ error) {
		h.nodeEndCalls++
	}
}

func (h *recordingGraphTracingHook) OnCheckpointSave(_ context.Context, _ string, _ int) func(error) {
	return func(_ error) {}
}

func (h *recordingGraphTracingHook) OnInterrupt(_ context.Context, _ string, _ InterruptType, _ int) {
}
func (h *recordingGraphTracingHook) OnResume(_ context.Context, _ string, _ int) {}
func (h *recordingGraphTracingHook) OnRewind(_ context.Context, _ string, _ int) {}

// ── recording agent metrics hook mock ────────────────────────────────────────

type recordingAgentMetricsHook struct {
	invokeStartCalls int
	invokeEndCalls   int
}

func (h *recordingAgentMetricsHook) OnInvokeStart() func(error, agent.TokenUsage) {
	h.invokeStartCalls++
	return func(_ error, _ agent.TokenUsage) {
		h.invokeEndCalls++
	}
}

func (h *recordingAgentMetricsHook) OnIterationStart() {}
func (h *recordingAgentMetricsHook) OnProviderCallStart(_ string) func(error, agent.TokenUsage) {
	return nil
}
func (h *recordingAgentMetricsHook) OnToolStart(_ string) func(error)     { return nil }
func (h *recordingAgentMetricsHook) OnGuardrailComplete(_ string, _ bool) {}
func (h *recordingAgentMetricsHook) OnImagesAttached(_ int)               {}
func (h *recordingAgentMetricsHook) OnDocumentsAttached(_ int)            {}

// ── recording graph metrics hook mock ────────────────────────────────────────

type recordingGraphMetricsHook struct {
	graphRunStartCalls int
	nodeStartCalls     int
	nodeEndCalls       int
}

func (h *recordingGraphMetricsHook) OnGraphRunStart() func(error, int) {
	h.graphRunStartCalls++
	return func(_ error, _ int) {}
}

func (h *recordingGraphMetricsHook) OnNodeStart(_ string) func(error) {
	h.nodeStartCalls++
	return func(_ error) {
		h.nodeEndCalls++
	}
}

// ── recording agent logging hook mock ────────────────────────────────────────

type recordingAgentLoggingHook struct {
	invokeStartCalls int
	invokeEndCalls   int
	toolStartCalls   int
	toolEndCalls     int
}

func (h *recordingAgentLoggingHook) OnInvokeStart(_ agent.InvokeSpanParams) { h.invokeStartCalls++ }
func (h *recordingAgentLoggingHook) OnInvokeEnd(_ error, _ agent.TokenUsage, _ time.Duration) {
	h.invokeEndCalls++
}
func (h *recordingAgentLoggingHook) OnIterationStart(_ int)       {}
func (h *recordingAgentLoggingHook) OnProviderCallStart(_ string) {}
func (h *recordingAgentLoggingHook) OnProviderCallEnd(_ error, _ agent.TokenUsage, _ int, _ time.Duration) {
}
func (h *recordingAgentLoggingHook) OnToolStart(_ string)                          { h.toolStartCalls++ }
func (h *recordingAgentLoggingHook) OnToolEnd(_ string, _ error, _ time.Duration)  { h.toolEndCalls++ }
func (h *recordingAgentLoggingHook) OnToolLog(_ string, _ string)                  {}
func (h *recordingAgentLoggingHook) OnGuardrailComplete(_ string, _ bool, _ error) {}
func (h *recordingAgentLoggingHook) OnConversationStart(_ string, _ string)        {}
func (h *recordingAgentLoggingHook) OnConversationEnd(_ string, _ string, _ error, _ int, _ time.Duration) {
}
func (h *recordingAgentLoggingHook) OnRetrieverStart(_ string)                      {}
func (h *recordingAgentLoggingHook) OnRetrieverEnd(_ error, _ int, _ time.Duration) {}
func (h *recordingAgentLoggingHook) OnImagesAttached(_ int)                         {}
func (h *recordingAgentLoggingHook) OnDocumentsAttached(_ int)                      {}
func (h *recordingAgentLoggingHook) OnMaxIterationsExceeded(_ int)                  {}
func (h *recordingAgentLoggingHook) OnStreamChunk(_ string)                         {}
func (h *recordingAgentLoggingHook) OnResponse(_ string)                            {}
