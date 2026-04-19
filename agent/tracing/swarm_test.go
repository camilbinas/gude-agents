package tracing

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/codes"

	agent "github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestSwarmTracing_RunSpanCreated(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	// Two agents: alpha answers directly, beta never runs.
	alphaProv := newMockProvider(
		&agent.ProviderResponse{Text: "alpha reply"},
	)
	betaProv := newMockProvider(
		&agent.ProviderResponse{Text: "beta reply"},
	)

	alpha, _ := agent.New(alphaProv, prompt.Text("alpha"), nil, WithTracing(tp))
	beta, _ := agent.New(betaProv, prompt.Text("beta"), nil, WithTracing(tp))

	sw, err := agent.NewSwarm([]agent.SwarmMember{
		{Name: "alpha", Description: "Alpha agent", Agent: alpha},
		{Name: "beta", Description: "Beta agent", Agent: beta},
	}, WithSwarmTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	result, err := sw.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAgent != "alpha" {
		t.Errorf("expected final agent alpha, got %q", result.FinalAgent)
	}

	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()

	// Should have a swarm.run span.
	swarmSpan := findSpan(spans, "swarm.run")
	if swarmSpan == nil {
		t.Fatal("expected swarm.run span")
	}
	if swarmSpan.Status.Code != codes.Ok {
		t.Errorf("expected swarm.run status Ok, got %v", swarmSpan.Status.Code)
	}

	// Should have a swarm.agent.alpha span.
	agentSpan := findSpan(spans, "swarm.agent.alpha")
	if agentSpan == nil {
		t.Fatal("expected swarm.agent.alpha span")
	}

	// Agent span should be a child of swarm.run.
	if agentSpan.Parent.SpanID() != swarmSpan.SpanContext.SpanID() {
		t.Error("swarm.agent.alpha should be a child of swarm.run")
	}

	// Should also have agent.iteration and agent.provider.call as children
	// of swarm.agent.alpha (the swarm's runAgent now instruments these).
	iterSpan := findSpan(spans, "agent.iteration")
	if iterSpan == nil {
		t.Fatal("expected agent.iteration span under swarm.agent.alpha")
	}
	if iterSpan.Parent.SpanID() != agentSpan.SpanContext.SpanID() {
		t.Error("agent.iteration should be a child of swarm.agent.alpha")
	}

	provSpan := findSpan(spans, "agent.provider.call")
	if provSpan == nil {
		t.Fatal("expected agent.provider.call span under agent.iteration")
	}
	if provSpan.Parent.SpanID() != iterSpan.SpanContext.SpanID() {
		t.Error("agent.provider.call should be a child of agent.iteration")
	}
}

func TestSwarmTracing_HandoffSpans(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	// Alpha hands off to beta, beta answers.
	alphaProv := newMockProvider(
		&agent.ProviderResponse{ToolCalls: []tool.Call{
			{ToolUseID: "tc1", Name: "transfer_to_beta", Input: json.RawMessage(`{"summary":"need beta"}`)},
		}},
	)
	betaProv := newMockProvider(
		&agent.ProviderResponse{Text: "beta handled it"},
	)

	alpha, _ := agent.New(alphaProv, prompt.Text("alpha"), nil, WithTracing(tp))
	beta, _ := agent.New(betaProv, prompt.Text("beta"), nil, WithTracing(tp))

	sw, err := agent.NewSwarm([]agent.SwarmMember{
		{Name: "alpha", Description: "Alpha agent", Agent: alpha},
		{Name: "beta", Description: "Beta agent", Agent: beta},
	}, WithSwarmTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	result, err := sw.Invoke(context.Background(), "help me")
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAgent != "beta" {
		t.Errorf("expected final agent beta, got %q", result.FinalAgent)
	}
	if len(result.HandoffHistory) != 1 {
		t.Fatalf("expected 1 handoff, got %d", len(result.HandoffHistory))
	}

	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()

	// Should have swarm.run span.
	swarmSpan := findSpan(spans, "swarm.run")
	if swarmSpan == nil {
		t.Fatal("expected swarm.run span")
	}

	// Should have both agent spans.
	alphaSpan := findSpan(spans, "swarm.agent.alpha")
	if alphaSpan == nil {
		t.Fatal("expected swarm.agent.alpha span")
	}
	betaSpan := findSpan(spans, "swarm.agent.beta")
	if betaSpan == nil {
		t.Fatal("expected swarm.agent.beta span")
	}

	// Both should be children of swarm.run.
	if alphaSpan.Parent.SpanID() != swarmSpan.SpanContext.SpanID() {
		t.Error("swarm.agent.alpha should be a child of swarm.run")
	}
	if betaSpan.Parent.SpanID() != swarmSpan.SpanContext.SpanID() {
		t.Error("swarm.agent.beta should be a child of swarm.run")
	}

	// swarm.run should have a handoff event.
	if !hasEvent(*swarmSpan, "swarm.handoff") {
		t.Error("expected swarm.handoff event on swarm.run span")
	}

	// Should have tool spans for the handoff tool under alpha's iteration.
	toolSpan := findSpan(spans, "agent.tool.transfer_to_beta")
	if toolSpan == nil {
		t.Fatal("expected agent.tool.transfer_to_beta span")
	}

	// Check handoff count attribute.
	v := getAttr(*swarmSpan, AttrSwarmHandoffCount)
	if v.AsInt64() != 1 {
		t.Errorf("expected handoff_count=1, got %d", v.AsInt64())
	}

	// Check final agent attribute.
	v = getAttr(*swarmSpan, AttrSwarmFinalAgent)
	if v.AsString() != "beta" {
		t.Errorf("expected final_agent=beta, got %q", v.AsString())
	}
}

func TestSwarmTracing_ErrorStatus(t *testing.T) {
	exp, tp := newTestTracerProvider()
	defer tp.Shutdown(context.Background())

	// Alpha's provider returns an error.
	errProv := &errorProvider{err: fmt.Errorf("provider down")}
	betaProv := newMockProvider(&agent.ProviderResponse{Text: "ok"})

	alpha, _ := agent.New(errProv, prompt.Text("alpha"), nil, WithTracing(tp))
	beta, _ := agent.New(betaProv, prompt.Text("beta"), nil, WithTracing(tp))

	sw, err := agent.NewSwarm([]agent.SwarmMember{
		{Name: "alpha", Description: "Alpha agent", Agent: alpha},
		{Name: "beta", Description: "Beta agent", Agent: beta},
	}, WithSwarmTracing(tp))
	if err != nil {
		t.Fatal(err)
	}

	_, err = sw.Invoke(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error")
	}

	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()

	// swarm.run should have error status.
	swarmSpan := findSpan(spans, "swarm.run")
	if swarmSpan == nil {
		t.Fatal("expected swarm.run span")
	}
	if swarmSpan.Status.Code != codes.Error {
		t.Errorf("expected swarm.run error status, got %v", swarmSpan.Status.Code)
	}

	// swarm.agent.alpha should have error status.
	agentSpan := findSpan(spans, "swarm.agent.alpha")
	if agentSpan == nil {
		t.Fatal("expected swarm.agent.alpha span")
	}
	if agentSpan.Status.Code != codes.Error {
		t.Errorf("expected swarm.agent.alpha error status, got %v", agentSpan.Status.Code)
	}
}
