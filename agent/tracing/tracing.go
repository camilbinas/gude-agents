// Package tracing provides OpenTelemetry instrumentation for gude-agents.
//
// Enable tracing by passing WithTracing as an agent.Option:
//
//	a, err := agent.New(provider, instructions, tools, tracing.WithTracing(tp))
//
// When tp is nil, the global TracerProvider is used.
//
// To capture prompts, responses, and tool I/O in span attributes (opt-in):
//
//	a, err := agent.New(provider, instructions, tools,
//	    tracing.WithTracing(tp, tracing.WithContentCapture()),
//	)
package tracing

import (
	"context"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	agent "github.com/camilbinas/gude-agents/agent"
)

const instrumentationName = "github.com/camilbinas/gude-agents"

// TracingOption configures the tracing hook behavior.
type TracingOption func(*otelHook)

// WithContentCapture enables recording of prompts, responses, tool inputs/outputs,
// and guardrail text as span attributes. This is opt-in because these can contain
// sensitive data (PII, secrets, proprietary content).
//
// When enabled, the following attributes are added:
//   - gen_ai.prompt (user message on agent.invoke)
//   - gen_ai.system (system prompt on agent.invoke)
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

// otelHook implements agent.TracingHook using OpenTelemetry spans.
type otelHook struct {
	tracer         trace.Tracer
	captureContent bool
	scheme         AttributeScheme
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

// Compile-time check that otelHook implements agent.TracingHook.
var _ agent.TracingHook = (*otelHook)(nil)

// WithTracing returns an agent.Option that enables OpenTelemetry tracing.
// If tp is nil, the global TracerProvider is used.
func WithTracing(tp trace.TracerProvider, opts ...TracingOption) agent.Option {
	return func(a *agent.Agent) error {
		if tp == nil {
			tp = otel.GetTracerProvider()
		}
		tracer := tp.Tracer(instrumentationName)
		a.SetTracingHook(newOtelHook(tracer, opts...))
		return nil
	}
}

func (h *otelHook) OnInvokeStart(ctx context.Context, params agent.InvokeSpanParams) (context.Context, func(error, agent.TokenUsage, string)) {
	ctx, span := h.tracer.Start(ctx, "agent.invoke")
	span.SetAttributes(
		attribute.Int(h.scheme.Key(RoleAgentMaxIterations), params.MaxIterations),
		attribute.String(h.scheme.Key(RoleGenAISystem), "gude-agents"),
	)
	if params.ModelID != "" {
		span.SetAttributes(attribute.String(h.scheme.Key(RoleAgentModelID), params.ModelID))
	}
	if params.ConversationID != "" {
		span.SetAttributes(attribute.String(h.scheme.Key(RoleAgentConversationID), params.ConversationID))
	}
	if params.AgentName != "" {
		span.SetAttributes(attribute.String(h.scheme.Key(RoleAgentName), params.AgentName))
	}
	if params.ImageCount > 0 {
		span.SetAttributes(attribute.Int(h.scheme.Key(RoleAgentImageCount), params.ImageCount))
	}
	if params.DocumentCount > 0 {
		span.SetAttributes(attribute.Int(h.scheme.Key(RoleAgentDocumentCount), params.DocumentCount))
	}
	if h.captureContent {
		if params.UserMessage != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleGenAIPrompt), params.UserMessage))
		}
		if params.SystemPrompt != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleGenAISystemPrompt), params.SystemPrompt))
		}
	}
	// Record inference config parameters when set.
	if cfg := params.InferenceConfig; cfg != nil {
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
	return ctx, func(err error, usage agent.TokenUsage, response string) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
			span.SetAttributes(
				attribute.Int(h.scheme.Key(RoleAgentTokenInput), usage.InputTokens),
				attribute.Int(h.scheme.Key(RoleAgentTokenOutput), usage.OutputTokens),
			)
			if usage.CacheReadTokens > 0 {
				span.SetAttributes(attribute.Int(h.scheme.Key(RoleAgentTokenCacheRead), usage.CacheReadTokens))
			}
			if usage.CacheWriteTokens > 0 {
				span.SetAttributes(attribute.Int(h.scheme.Key(RoleAgentTokenCacheWrite), usage.CacheWriteTokens))
			}
			if h.captureContent && response != "" {
				span.SetAttributes(attribute.String(h.scheme.Key(RoleGenAICompletion), response))
			}
		}
		span.End()
	}
}

func (h *otelHook) OnIterationStart(ctx context.Context, iteration int) (context.Context, func(toolCount int, isFinal bool)) {
	ctx, span := h.tracer.Start(ctx, "agent.iteration")
	span.SetAttributes(attribute.Int(h.scheme.Key(RoleIterationNumber), iteration))
	return ctx, func(toolCount int, isFinal bool) {
		span.SetAttributes(
			attribute.Int(h.scheme.Key(RoleIterationToolCount), toolCount),
			attribute.Bool(h.scheme.Key(RoleIterationFinal), isFinal),
		)
		span.End()
	}
}

func (h *otelHook) OnProviderCallStart(ctx context.Context, params agent.ProviderCallParams) (context.Context, func(err error, usage agent.TokenUsage, toolCallCount int, responseText string)) {
	ctx, span := h.tracer.Start(ctx, "agent.provider.call")
	if h.captureContent {
		span.SetAttributes(attribute.Int(h.scheme.Key(RoleProviderMessageCount), params.MessageCount))
	}
	// Record inference config parameters when set.
	if cfg := params.InferenceConfig; cfg != nil {
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
	return ctx, func(err error, usage agent.TokenUsage, toolCallCount int, responseText string) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetAttributes(
				attribute.Int(h.scheme.Key(RoleProviderInputTokens), usage.InputTokens),
				attribute.Int(h.scheme.Key(RoleProviderOutputTokens), usage.OutputTokens),
				attribute.Int(h.scheme.Key(RoleProviderToolCalls), toolCallCount),
			)
			if usage.CacheReadTokens > 0 {
				span.SetAttributes(attribute.Int(h.scheme.Key(RoleProviderCacheReadTokens), usage.CacheReadTokens))
			}
			if usage.CacheWriteTokens > 0 {
				span.SetAttributes(attribute.Int(h.scheme.Key(RoleProviderCacheWriteTokens), usage.CacheWriteTokens))
			}
			if h.captureContent && responseText != "" {
				span.SetAttributes(attribute.String(h.scheme.Key(RoleGenAIProviderResponse), responseText))
			}
		}
		span.End()
	}
}

func (h *otelHook) OnToolStart(ctx context.Context, toolName string, input json.RawMessage) (context.Context, func(err error, output string)) {
	ctx, span := h.tracer.Start(ctx, fmt.Sprintf("agent.tool.%s", toolName))
	span.SetAttributes(attribute.String(h.scheme.Key(RoleToolName), toolName))
	if h.captureContent && len(input) > 0 {
		span.SetAttributes(attribute.String(h.scheme.Key(RoleToolInput), string(input)))
	}
	return ctx, func(err error, output string) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if h.captureContent && output != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleToolOutput), output))
		}
		span.End()
	}
}

func (h *otelHook) OnGuardrailStart(ctx context.Context, direction string, input string) (context.Context, func(err error, output string)) {
	spanName := fmt.Sprintf("agent.guardrail.%s", direction)
	ctx, span := h.tracer.Start(ctx, spanName)
	if h.captureContent && input != "" {
		span.SetAttributes(attribute.String(h.scheme.Key(RoleGuardrailInput), input))
	}
	return ctx, func(err error, output string) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if h.captureContent && output != "" {
			span.SetAttributes(attribute.String(h.scheme.Key(RoleGuardrailOutput), output))
		}
		span.End()
	}
}

func (h *otelHook) OnConversationStart(ctx context.Context, operation string, conversationID string) (context.Context, func(err error)) {
	spanName := fmt.Sprintf("agent.conversation.%s", operation)
	ctx, span := h.tracer.Start(ctx, spanName)
	span.SetAttributes(attribute.String(h.scheme.Key(RoleMemoryConversationID), conversationID))
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
}

func (h *otelHook) OnRetrieverStart(ctx context.Context, query string) (context.Context, func(err error, docCount int)) {
	ctx, span := h.tracer.Start(ctx, "agent.retriever.retrieve")
	if h.captureContent && query != "" {
		span.SetAttributes(attribute.String(h.scheme.Key(RoleRetrieverQuery), query))
	}
	return ctx, func(err error, docCount int) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetAttributes(attribute.Int(h.scheme.Key(RoleRetrieverDocumentCount), docCount))
		}
		span.End()
	}
}

func (h *otelHook) OnMaxIterationsExceeded(ctx context.Context, limit int) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(h.scheme.Key(RoleEventMaxIterationsExceeded), trace.WithAttributes(
		attribute.Int(h.scheme.Key(RoleAgentMaxIterations), limit),
	))
}
