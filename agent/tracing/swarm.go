package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	agent "github.com/camilbinas/gude-agents/agent"
)

// Swarm-specific attribute keys.
const (
	AttrSwarmInitialAgent = "swarm.initial_agent"
	AttrSwarmFinalAgent   = "swarm.final_agent"
	AttrSwarmMemberCount  = "swarm.member_count"
	AttrSwarmMaxHandoffs  = "swarm.max_handoffs"
	AttrSwarmHandoffCount = "swarm.handoff_count"
	AttrSwarmHandoffFrom  = "swarm.handoff.from"
	AttrSwarmHandoffTo    = "swarm.handoff.to"
	AttrSwarmAgentName    = "swarm.agent.name"
)

// swarmOtelHook implements agent.SwarmTracingHook using OpenTelemetry spans.
type swarmOtelHook struct {
	tracer trace.Tracer
}

// Compile-time check.
var _ agent.SwarmTracingHook = (*swarmOtelHook)(nil)

// WithSwarmTracing returns an agent.SwarmOption that enables OpenTelemetry tracing
// for swarm execution. If tp is nil, the global TracerProvider is used.
// Creates spans named "swarm.run" and "swarm.agent.<name>" with handoff events.
func WithSwarmTracing(tp trace.TracerProvider) agent.SwarmOption {
	return func(s *agent.Swarm) error {
		if tp == nil {
			tp = otel.GetTracerProvider()
		}
		tracer := tp.Tracer(instrumentationName)
		s.SetSwarmTracingHook(&swarmOtelHook{tracer: tracer})
		return nil
	}
}

func (h *swarmOtelHook) OnSwarmRunStart(ctx context.Context, params agent.SwarmRunSpanParams) (context.Context, func(error, agent.SwarmResult)) {
	ctx, span := h.tracer.Start(ctx, "swarm.run")
	span.SetAttributes(
		attribute.String(AttrSwarmInitialAgent, params.InitialAgent),
		attribute.Int(AttrSwarmMemberCount, params.MemberCount),
		attribute.Int(AttrSwarmMaxHandoffs, params.MaxHandoffs),
	)
	if params.ConversationID != "" {
		span.SetAttributes(attribute.String(AttrAgentConversationID, params.ConversationID))
	}
	return ctx, func(err error, result agent.SwarmResult) {
		span.SetAttributes(
			attribute.Int(AttrSwarmHandoffCount, len(result.HandoffHistory)),
			attribute.Int(AttrAgentTokenUsageInput, result.Usage.InputTokens),
			attribute.Int(AttrAgentTokenUsageOutput, result.Usage.OutputTokens),
		)
		if result.FinalAgent != "" {
			span.SetAttributes(attribute.String(AttrSwarmFinalAgent, result.FinalAgent))
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}
}

func (h *swarmOtelHook) OnSwarmAgentStart(ctx context.Context, agentName string) (context.Context, func(error)) {
	ctx, span := h.tracer.Start(ctx, fmt.Sprintf("swarm.agent.%s", agentName))
	span.SetAttributes(attribute.String(AttrSwarmAgentName, agentName))
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
}

func (h *swarmOtelHook) OnSwarmHandoff(ctx context.Context, from, to string) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent("swarm.handoff", trace.WithAttributes(
		attribute.String(AttrSwarmHandoffFrom, from),
		attribute.String(AttrSwarmHandoffTo, to),
	))
}
