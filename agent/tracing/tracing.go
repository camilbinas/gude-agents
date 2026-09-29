// Package tracing provides OpenTelemetry instrumentation for gude-agents.
//
// Enable tracing by passing WithTracing as an agent.Option:
//
//	a, err := agent.New(provider, instructions,
//	    agent.WithTools(tools...),
//	    tracing.WithTracing(tp),
//	)
//
// When tp is nil, the global TracerProvider is used.
//
// To capture prompts, responses, and tool I/O in span attributes (opt-in):
//
//	a, err := agent.New(provider, instructions,
//	    tracing.WithTracing(tp, tracing.WithContentCapture()),
//	)
package tracing

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	agent "github.com/camilbinas/gude-agents/agent"
)

const instrumentationName = "github.com/camilbinas/gude-agents"

// TracingOption configures the tracing observer behavior.
type TracingOption func(*otelHook)

// WithContentCapture enables recording of prompts, responses, tool inputs/outputs,
// and guardrail text as span attributes. This is opt-in because these can contain
// sensitive data (PII, secrets, proprietary content).
//
// When enabled, the following attributes are added:
//   - gen_ai.prompt (user message on agent.invoke)
//   - gen_ai.system_prompt (system prompt on agent.invoke)
//   - gen_ai.completion (response text on agent.invoke)
//   - gen_ai.provider.response (response text on agent.provider.call)
//   - tool.input / tool.output (on agent.tool.* spans)
//   - guardrail.input / guardrail.output (on guardrail spans)
//   - retriever.query (on retriever spans)
func WithContentCapture() TracingOption {
	return func(h *otelHook) {
		h.captureContent = true
	}
}

// WithScopeName overrides the OpenTelemetry instrumentation scope name.
// Default: "github.com/camilbinas/gude-agents".
func WithScopeName(name string) TracingOption {
	return func(h *otelHook) {
		h.scopeName = name
	}
}

// otelHook implements the tracing-related observer capabilities with
// OpenTelemetry spans.
type otelHook struct {
	tracer         trace.Tracer
	captureContent bool
	scopeName      string
	scheme         AttributeScheme
}

type spanContextKey struct {
	owner      *otelHook
	capability string
}

// newOtelHook constructs an otelHook with the given tracer and options applied.
// The scheme is left as a nil map by default; AttributeScheme.Key falls back
// to each role's default key when the map is nil or missing an entry.
func newOtelHook(tracer trace.Tracer, opts ...TracingOption) *otelHook {
	h := &otelHook{tracer: tracer}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

var (
	_ agent.InvokeObserver       = (*otelHook)(nil)
	_ agent.IterationObserver    = (*otelHook)(nil)
	_ agent.ModelObserver        = (*otelHook)(nil)
	_ agent.ToolObserver         = (*otelHook)(nil)
	_ agent.GuardrailObserver    = (*otelHook)(nil)
	_ agent.ConversationObserver = (*otelHook)(nil)
	_ agent.RetrievalObserver    = (*otelHook)(nil)
	_ agent.LimitObserver        = (*otelHook)(nil)
)

// WithTracing returns an agent.Option that enables OpenTelemetry tracing.
// If tp is nil, the global TracerProvider is used.
func WithTracing(tp trace.TracerProvider, opts ...TracingOption) agent.Option {
	return func(a *agent.Agent) error {
		if tp == nil {
			tp = otel.GetTracerProvider()
		}
		h := &otelHook{}
		for _, opt := range opts {
			opt(h)
		}
		scopeName := instrumentationName
		if h.scopeName != "" {
			scopeName = h.scopeName
		}
		h.tracer = tp.Tracer(scopeName)
		return agent.WithObserver(h)(a)
	}
}

func (h *otelHook) startSpan(ctx context.Context, capability, name string, timestamp time.Time) (context.Context, trace.Span) {
	var opts []trace.SpanStartOption
	if !timestamp.IsZero() {
		opts = append(opts, trace.WithTimestamp(timestamp))
	}
	ctx, span := h.tracer.Start(ctx, name, opts...)
	return context.WithValue(ctx, spanContextKey{owner: h, capability: capability}, span), span
}

func (h *otelHook) spanFromContext(ctx context.Context, capability string) trace.Span {
	span, _ := ctx.Value(spanContextKey{owner: h, capability: capability}).(trace.Span)
	return span
}

func endSpan(span trace.Span, timestamp time.Time) {
	if span == nil {
		return
	}
	if timestamp.IsZero() {
		span.End()
		return
	}
	span.End(trace.WithTimestamp(timestamp))
}

func recordError(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func (h *otelHook) setInferenceAttributes(span trace.Span, cfg *agent.InferenceConfig) {
	if cfg == nil {
		return
	}
	if cfg.Temperature != nil {
		span.SetAttributes(attribute.Float64(h.scheme.Key(RoleGenAITemperature), *cfg.Temperature))
	}
	if cfg.TopP != nil {
		span.SetAttributes(attribute.Float64(h.scheme.Key(RoleGenAITopP), *cfg.TopP))
	}
	if cfg.TopK != nil {
		span.SetAttributes(attribute.Int(h.scheme.Key(RoleGenAITopK), *cfg.TopK))
	}
	if cfg.MaxTokens != nil {
		span.SetAttributes(attribute.Int(h.scheme.Key(RoleGenAIMaxTokens), *cfg.MaxTokens))
	}
	if cfg.StopSequences != nil {
		span.SetAttributes(attribute.StringSlice(h.scheme.Key(RoleGenAIStopSequences), cfg.StopSequences))
	}
}

// ObserveInvoke traces a complete agent invocation.
func (h *otelHook) ObserveInvoke(ctx context.Context, record agent.InvokeRecord) context.Context {
	const capability = "invoke"
	if record.Phase == agent.Start {
		ctx, span := h.startSpan(ctx, capability, "agent.invoke", record.Timestamp)
		span.SetAttributes(
			attribute.Int(h.scheme.Key(RoleAgentMaxIterations), record.MaxIterations),
			attribute.String(h.scheme.Key(RoleGenAISystem), "gude-agents"),
		)
		if key := h.scheme.Key(RoleOperationName); key != "" {
			span.SetAttributes(attribute.String(key, "invoke_agent"))
		}
		if record.ModelID != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleAgentModelID), record.ModelID))
		}
		if record.ConversationID != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleAgentConversationID), record.ConversationID))
		}
		if record.AgentName != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleAgentName), record.AgentName))
		}
		if record.ImageCount > 0 {
			span.SetAttributes(attribute.Int(h.scheme.Key(RoleAgentImageCount), record.ImageCount))
		}
		if record.DocumentCount > 0 {
			span.SetAttributes(attribute.Int(h.scheme.Key(RoleAgentDocumentCount), record.DocumentCount))
		}
		if h.captureContent {
			if record.UserMessage != "" {
				span.SetAttributes(attribute.String(h.scheme.Key(RoleGenAIPrompt), record.UserMessage))
			}
			if record.SystemPrompt != "" {
				span.SetAttributes(attribute.String(h.scheme.Key(RoleGenAISystemPrompt), record.SystemPrompt))
			}
		}
		h.setInferenceAttributes(span, record.InferenceConfig)
		return ctx
	}
	if record.Phase != agent.End {
		return ctx
	}

	span := h.spanFromContext(ctx, capability)
	if span == nil {
		return ctx
	}
	if h.captureContent && record.Response != "" {
		span.SetAttributes(attribute.String(h.scheme.Key(RoleGenAICompletion), record.Response))
	}
	if record.Err != nil {
		recordError(span, record.Err)
	} else {
		span.SetStatus(codes.Ok, "")
		span.SetAttributes(
			attribute.Int(h.scheme.Key(RoleAgentTokenInput), record.Usage.InputTokens),
			attribute.Int(h.scheme.Key(RoleAgentTokenOutput), record.Usage.OutputTokens),
		)
		if record.Usage.CacheReadTokens > 0 {
			span.SetAttributes(attribute.Int(h.scheme.Key(RoleAgentTokenCacheRead), record.Usage.CacheReadTokens))
		}
		if record.Usage.CacheWriteTokens > 0 {
			span.SetAttributes(attribute.Int(h.scheme.Key(RoleAgentTokenCacheWrite), record.Usage.CacheWriteTokens))
		}
	}
	endSpan(span, record.Timestamp)
	return ctx
}

// ObserveIteration traces one model/tool loop iteration.
func (h *otelHook) ObserveIteration(ctx context.Context, record agent.IterationRecord) context.Context {
	const capability = "iteration"
	if record.Phase == agent.Start {
		ctx, span := h.startSpan(ctx, capability, "agent.iteration", record.Timestamp)
		span.SetAttributes(attribute.Int(h.scheme.Key(RoleIterationNumber), record.Iteration))
		return ctx
	}
	if record.Phase != agent.End {
		return ctx
	}

	span := h.spanFromContext(ctx, capability)
	if span == nil {
		return ctx
	}
	span.SetAttributes(
		attribute.Int(h.scheme.Key(RoleIterationToolCount), record.ToolCount),
		attribute.Bool(h.scheme.Key(RoleIterationFinal), record.IsFinal),
	)
	recordError(span, record.Err)
	endSpan(span, record.Timestamp)
	return ctx
}

// ObserveModel traces one logical provider call.
func (h *otelHook) ObserveModel(ctx context.Context, record agent.ModelCallRecord) context.Context {
	const capability = "model"
	if record.Phase == agent.Start {
		ctx, span := h.startSpan(ctx, capability, "agent.provider.call", record.Timestamp)
		if key := h.scheme.Key(RoleOperationName); key != "" {
			span.SetAttributes(attribute.String(key, "chat"))
		}
		if record.ModelID != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleProviderModelID), record.ModelID))
		}
		if h.captureContent {
			span.SetAttributes(attribute.Int(h.scheme.Key(RoleProviderMessageCount), record.MessageCount))
		}
		h.setInferenceAttributes(span, record.InferenceConfig)
		return ctx
	}
	if record.Phase != agent.End {
		return ctx
	}

	span := h.spanFromContext(ctx, capability)
	if span == nil {
		return ctx
	}
	if record.Err != nil {
		recordError(span, record.Err)
	} else {
		span.SetAttributes(
			attribute.Int(h.scheme.Key(RoleProviderInputTokens), record.Usage.InputTokens),
			attribute.Int(h.scheme.Key(RoleProviderOutputTokens), record.Usage.OutputTokens),
			attribute.Int(h.scheme.Key(RoleProviderToolCalls), record.ToolCallCount),
		)
		if record.Usage.CacheReadTokens > 0 {
			span.SetAttributes(attribute.Int(h.scheme.Key(RoleProviderCacheReadTokens), record.Usage.CacheReadTokens))
		}
		if record.Usage.CacheWriteTokens > 0 {
			span.SetAttributes(attribute.Int(h.scheme.Key(RoleProviderCacheWriteTokens), record.Usage.CacheWriteTokens))
		}
		if h.captureContent && record.ResponseText != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleGenAIProviderResponse), record.ResponseText))
		}
	}
	endSpan(span, record.Timestamp)
	return ctx
}

// ObserveTool traces one attempted tool call.
func (h *otelHook) ObserveTool(ctx context.Context, record agent.ToolCallRecord) context.Context {
	const capability = "tool"
	if record.Phase == agent.Start {
		ctx, span := h.startSpan(ctx, capability, fmt.Sprintf("agent.tool.%s", record.Name), record.Timestamp)
		span.SetAttributes(attribute.String(h.scheme.Key(RoleToolName), record.Name))
		if key := h.scheme.Key(RoleOperationName); key != "" {
			span.SetAttributes(attribute.String(key, "execute_tool"))
		}
		if h.captureContent && len(record.Input) > 0 {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleToolInput), string(record.Input)))
		}
		return ctx
	}
	if record.Phase != agent.End {
		return ctx
	}

	span := h.spanFromContext(ctx, capability)
	if span == nil {
		return ctx
	}
	if record.Err != nil {
		recordError(span, record.Err)
	} else {
		if record.ResultIsError {
			span.SetStatus(codes.Error, "tool result error")
		}
		if h.captureContent && record.Output != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleToolOutput), record.Output))
		}
	}
	endSpan(span, record.Timestamp)
	return ctx
}

// ObserveGuardrail traces one input or output guardrail evaluation.
func (h *otelHook) ObserveGuardrail(ctx context.Context, record agent.GuardrailRecord) context.Context {
	const capability = "guardrail"
	if record.Phase == agent.Start {
		ctx, span := h.startSpan(ctx, capability, fmt.Sprintf("agent.guardrail.%s", record.Direction), record.Timestamp)
		if h.captureContent && record.Input != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleGuardrailInput), record.Input))
		}
		return ctx
	}
	if record.Phase != agent.End {
		return ctx
	}

	span := h.spanFromContext(ctx, capability)
	if span == nil {
		return ctx
	}
	if record.Err != nil {
		recordError(span, record.Err)
	} else if h.captureContent && record.Output != "" {
		span.SetAttributes(attribute.String(h.scheme.Key(RoleGuardrailOutput), record.Output))
	}
	endSpan(span, record.Timestamp)
	return ctx
}

// ObserveConversation traces one conversation-store operation.
func (h *otelHook) ObserveConversation(ctx context.Context, record agent.ConversationRecord) context.Context {
	const capability = "conversation"
	if record.Phase == agent.Start {
		ctx, span := h.startSpan(ctx, capability, fmt.Sprintf("agent.conversation.%s", record.Operation), record.Timestamp)
		span.SetAttributes(attribute.String(h.scheme.Key(RoleMemoryConversationID), record.ConversationID))
		return ctx
	}
	if record.Phase != agent.End {
		return ctx
	}

	span := h.spanFromContext(ctx, capability)
	if span == nil {
		return ctx
	}
	recordError(span, record.Err)
	endSpan(span, record.Timestamp)
	return ctx
}

// ObserveRetrieval traces one retrieval operation.
func (h *otelHook) ObserveRetrieval(ctx context.Context, record agent.RetrievalRecord) context.Context {
	const capability = "retrieval"
	if record.Phase == agent.Start {
		ctx, span := h.startSpan(ctx, capability, "agent.retriever.retrieve", record.Timestamp)
		if h.captureContent && record.Query != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleRetrieverQuery), record.Query))
		}
		return ctx
	}
	if record.Phase != agent.End {
		return ctx
	}

	span := h.spanFromContext(ctx, capability)
	if span == nil {
		return ctx
	}
	if record.Err != nil {
		recordError(span, record.Err)
	} else {
		span.SetAttributes(attribute.Int(h.scheme.Key(RoleRetrieverDocumentCount), record.DocumentCount))
	}
	endSpan(span, record.Timestamp)
	return ctx
}

// ObserveLimit records max-iteration exhaustion on the active invocation span.
func (h *otelHook) ObserveLimit(ctx context.Context, record agent.LimitRecord) context.Context {
	if record.Phase != agent.End || record.Name != "max_iterations" {
		return ctx
	}
	span := h.spanFromContext(ctx, "invoke")
	if span == nil {
		return ctx
	}
	eventOptions := []trace.EventOption{trace.WithAttributes(
		attribute.Int(h.scheme.Key(RoleAgentMaxIterations), record.Limit),
	)}
	if !record.Timestamp.IsZero() {
		eventOptions = append(eventOptions, trace.WithTimestamp(record.Timestamp))
	}
	span.AddEvent(h.scheme.Key(RoleEventMaxIterationsExceeded), eventOptions...)
	return ctx
}
