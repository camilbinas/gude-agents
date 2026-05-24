package agentcore

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// withGlobalTracerProvider temporarily installs tp as the global
// TracerProvider so that auto-wired tracing (which uses the global
// provider when called with nil) records spans into the in-memory
// exporter under the test's control.
func withGlobalTracerProvider(t *testing.T, tp *sdktrace.TracerProvider) func() {
	t.Helper()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return func() {
		otel.SetTracerProvider(prev)
	}
}

// stubTracingHook is a no-op agent.TracingHook used to verify that an
// existing hook is preserved by autoWireTracing.
type stubTracingHook struct{}

func (stubTracingHook) OnInvokeStart(ctx context.Context, _ agent.InvokeSpanParams) (context.Context, func(error, agent.TokenUsage, string)) {
	return ctx, func(error, agent.TokenUsage, string) {}
}
func (stubTracingHook) OnIterationStart(ctx context.Context, _ int) (context.Context, func(int, bool)) {
	return ctx, func(int, bool) {}
}
func (stubTracingHook) OnProviderCallStart(ctx context.Context, _ agent.ProviderCallParams) (context.Context, func(error, agent.TokenUsage, int, string)) {
	return ctx, func(error, agent.TokenUsage, int, string) {}
}
func (stubTracingHook) OnToolStart(ctx context.Context, _ string, _ json.RawMessage) (context.Context, func(error, string)) {
	return ctx, func(error, string) {}
}
func (stubTracingHook) OnGuardrailStart(ctx context.Context, _ string, _ string) (context.Context, func(error, string)) {
	return ctx, func(error, string) {}
}
func (stubTracingHook) OnConversationStart(ctx context.Context, _ string, _ string) (context.Context, func(error)) {
	return ctx, func(error) {}
}
func (stubTracingHook) OnRetrieverStart(ctx context.Context, _ string) (context.Context, func(error, int)) {
	return ctx, func(error, int) {}
}
func (stubTracingHook) OnMaxIterationsExceeded(_ context.Context, _ int) {}

// Compile-time check.
var _ agent.TracingHook = stubTracingHook{}

func TestAutoWireTracing_SetsHookWhenAbsent(t *testing.T) {
	mock := &mockAgentCoreClient{}
	rt := newTestRuntime(t, mock)

	if rt.agent.TracingHook() != nil {
		t.Fatal("precondition: agent should not have a tracing hook initially")
	}

	rt.autoWireTracing()

	if rt.agent.TracingHook() == nil {
		t.Fatal("expected autoWireTracing to install a tracing hook on the agent")
	}
}

func TestAutoWireTracing_PreservesExistingHook(t *testing.T) {
	mock := &mockAgentCoreClient{}
	rt := newTestRuntime(t, mock)

	existing := stubTracingHook{}
	rt.agent.SetTracingHook(existing)

	rt.autoWireTracing()

	if got := rt.agent.TracingHook(); got != existing {
		t.Fatalf("expected existing tracing hook to be preserved, got %#v (existing=%#v)", got, existing)
	}
}

func TestAutoWireTracing_EmitsAgentCoreSchemeAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())
	defer withGlobalTracerProvider(t, tp)()

	mock := &mockAgentCoreClient{}
	rt := newTestRuntime(t, mock)

	if rt.agent.TracingHook() != nil {
		t.Fatal("precondition: agent should not have a tracing hook initially")
	}

	rt.autoWireTracing()

	hook := rt.agent.TracingHook()
	if hook == nil {
		t.Fatal("expected tracing hook to be installed")
	}

	// Drive a span through the hook and verify attribute keys follow the
	// AgentCoreScheme (gen_ai.* OTel GenAI semantic conventions).
	ctx, finish := hook.OnInvokeStart(context.Background(), agent.InvokeSpanParams{
		AgentName:      "auto-wire-test",
		ConversationID: "conv-1",
		MaxIterations:  5,
		ModelID:        "model-x",
	})
	_ = ctx
	finish(nil, agent.TokenUsage{InputTokens: 10, OutputTokens: 7}, "ok")

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one exported span")
	}

	// Find the agent.invoke span and verify it carries gen_ai.* keys
	// per the AgentCore scheme.
	var invoke *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "agent.invoke" {
			invoke = &spans[i]
			break
		}
	}
	if invoke == nil {
		t.Fatalf("expected agent.invoke span; got %d spans", len(spans))
	}

	keys := attrKeys(invoke.Attributes)

	// Required AgentCore-scheme keys for OnInvokeStart.
	required := []string{
		"gen_ai.agent.name",
		"gen_ai.conversation.id",
		"gen_ai.request.model",
		"gen_ai.agent.max_iterations",
		"gen_ai.provider.name",
		"gen_ai.usage.input_tokens",
		"gen_ai.usage.output_tokens",
	}
	for _, k := range required {
		if _, ok := keys[k]; !ok {
			t.Errorf("expected attribute %q on agent.invoke span; got keys=%v", k, sortedKeys(keys))
		}
	}

	// Make sure the auto-wired scheme is NOT emitting the default keys
	// (which would indicate the scheme wasn't applied).
	for _, bad := range []string{"agent.name", "agent.conversation_id", "agent.model_id"} {
		if _, ok := keys[bad]; ok {
			t.Errorf("did not expect default key %q to be emitted under AgentCoreScheme", bad)
		}
	}
}

func attrKeys(attrs []attribute.KeyValue) map[string]struct{} {
	out := make(map[string]struct{}, len(attrs))
	for _, kv := range attrs {
		out[string(kv.Key)] = struct{}{}
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
