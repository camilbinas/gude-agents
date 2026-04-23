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
		h := &otelHook{tracer: tracer}
		for _, opt := range opts {
			opt(h)
		}
		a.SetTracingHook(h)
		return nil
	}
}

func (h *otelHook) OnInvokeStart(ctx context.Context, params agent.InvokeSpanParams) (context.Context, func(error, agent.TokenUsage, string)) {
	ctx, span := h.tracer.Start(ctx, "agent.invoke")
	span.SetAttributes(
		attribute.Int(AttrAgentMaxIterations, params.MaxIterations),
		attribute.String(AttrGenAISystem, "gude-agents"),
	)
	if params.ModelID != "" {
		span.SetAttributes(attribute.String(AttrAgentModelID, params.ModelID))
	}
	if params.ConversationID != "" {
		span.SetAttributes(attribute.String(AttrAgentConversationID, params.ConversationID))
	}
	if params.AgentName != "" {
		span.SetAttributes(attribute.String(AttrAgentName, params.AgentName))
	}
	if params.ImageCount > 0 {
		span.SetAttributes(attribute.Int(AttrAgentImageCount, params.ImageCount))
	}
	if h.captureContent {
		if params.UserMessage != "" {
			span.SetAttributes(attribute.String(AttrGenAIPrompt, params.UserMessage))
		}
		if params.SystemPrompt != "" {
			span.SetAttributes(attribute.String(AttrGenAISystemPrompt, params.SystemPrompt))
		}
	}
	// Record inference config parameters when set.
	if cfg := params.InferenceConfig; cfg != nil {
		if cfg.Temperature != nil {
			span.SetAttributes(attribute.Float64(AttrGenAITemperature, *cfg.Temperature))
		}
		if cfg.TopP != nil {
			span.SetAttributes(attribute.Float64(AttrGenAITopP, *cfg.TopP))
		}
		if cfg.TopK != nil {
			span.SetAttributes(attribute.Int(AttrGenAITopK, *cfg.TopK))
		}
		if cfg.MaxTokens != nil {
			span.SetAttributes(attribute.Int(AttrGenAIMaxTokens, *cfg.MaxTokens))
		}
		if cfg.StopSequences != nil {
			span.SetAttributes(attribute.StringSlice(AttrGenAIStopSequences, cfg.StopSequences))
		}
	}
	return ctx, func(err error, usage agent.TokenUsage, response string) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
			span.SetAttributes(
				attribute.Int(AttrAgentTokenUsageInput, usage.InputTokens),
				attribute.Int(AttrAgentTokenUsageOutput, usage.OutputTokens),
			)
			if h.captureContent && response != "" {
				span.SetAttributes(attribute.String(AttrGenAICompletion, response))
			}
		}
		span.End()
	}
}

func (h *otelHook) OnIterationStart(ctx context.Context, iteration int) (context.Context, func(toolCount int, isFinal bool)) {
	ctx, span := h.tracer.Start(ctx, "agent.iteration")
	span.SetAttributes(attribute.Int(AttrAgentIterationNumber, iteration))
	return ctx, func(toolCount int, isFinal bool) {
		span.SetAttributes(
			attribute.Int(AttrAgentIterationToolCount, toolCount),
			attribute.Bool(AttrAgentIterationFinal, isFinal),
		)
		span.End()
	}
}

func (h *otelHook) OnProviderCallStart(ctx context.Context, params agent.ProviderCallParams) (context.Context, func(err error, usage agent.TokenUsage, toolCallCount int, responseText string)) {
	ctx, span := h.tracer.Start(ctx, "agent.provider.call")
	if h.captureContent {
		span.SetAttributes(attribute.Int(AttrProviderMessageCount, params.MessageCount))
	}
	// Record inference config parameters when set.
	if cfg := params.InferenceConfig; cfg != nil {
		if cfg.Temperature != nil {
			span.SetAttributes(attribute.Float64(AttrGenAITemperature, *cfg.Temperature))
		}
		if cfg.TopP != nil {
			span.SetAttributes(attribute.Float64(AttrGenAITopP, *cfg.TopP))
		}
		if cfg.TopK != nil {
			span.SetAttributes(attribute.Int(AttrGenAITopK, *cfg.TopK))
		}
		if cfg.MaxTokens != nil {
			span.SetAttributes(attribute.Int(AttrGenAIMaxTokens, *cfg.MaxTokens))
		}
		if cfg.StopSequences != nil {
			span.SetAttributes(attribute.StringSlice(AttrGenAIStopSequences, cfg.StopSequences))
		}
	}
	return ctx, func(err error, usage agent.TokenUsage, toolCallCount int, responseText string) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetAttributes(
				attribute.Int(AttrProviderInputTokens, usage.InputTokens),
				attribute.Int(AttrProviderOutputTokens, usage.OutputTokens),
				attribute.Int(AttrProviderToolCalls, toolCallCount),
			)
			if h.captureContent && responseText != "" {
				span.SetAttributes(attribute.String(AttrGenAIProviderResponse, responseText))
			}
		}
		span.End()
	}
}

func (h *otelHook) OnToolStart(ctx context.Context, toolName string, input json.RawMessage) (context.Context, func(err error, output string)) {
	ctx, span := h.tracer.Start(ctx, fmt.Sprintf("agent.tool.%s", toolName))
	span.SetAttributes(attribute.String(AttrToolName, toolName))
	if h.captureContent && len(input) > 0 {
		span.SetAttributes(attribute.String(AttrToolInput, string(input)))
	}
	return ctx, func(err error, output string) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if h.captureContent && output != "" {
			span.SetAttributes(attribute.String(AttrToolOutput, output))
		}
		span.End()
	}
}

func (h *otelHook) OnGuardrailStart(ctx context.Context, direction string, input string) (context.Context, func(err error, output string)) {
	spanName := fmt.Sprintf("agent.guardrail.%s", direction)
	ctx, span := h.tracer.Start(ctx, spanName)
	if h.captureContent && input != "" {
		span.SetAttributes(attribute.String(AttrGuardrailInput, input))
	}
	return ctx, func(err error, output string) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if h.captureContent && output != "" {
			span.SetAttributes(attribute.String(AttrGuardrailOutput, output))
		}
		span.End()
	}
}

func (h *otelHook) OnMemoryStart(ctx context.Context, operation string, conversationID string) (context.Context, func(err error)) {
	spanName := fmt.Sprintf("agent.memory.%s", operation)
	ctx, span := h.tracer.Start(ctx, spanName)
	span.SetAttributes(attribute.String(AttrMemoryConversationID, conversationID))
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
		span.SetAttributes(attribute.String(AttrRetrieverQuery, query))
	}
	return ctx, func(err error, docCount int) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetAttributes(attribute.Int(AttrRetrieverDocumentCount, docCount))
		}
		span.End()
	}
}

func (h *otelHook) OnMaxIterationsExceeded(ctx context.Context, limit int) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(EventMaxIterationsExceeded, trace.WithAttributes(
		attribute.Int(AttrAgentMaxIterations, limit),
	))
}
