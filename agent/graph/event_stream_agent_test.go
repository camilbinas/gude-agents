package graph_test

import (
	"context"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/graph/checkpointer/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// memoryCheckpointer returns a fresh in-memory GraphCheckpointer.
func memoryCheckpointer(t *testing.T) graph.GraphCheckpointer {
	t.Helper()
	return memory.New()
}

// setterNode returns a NodeFunc[State] that writes a fixed key/value pair.
func setterNode(key, val string) graph.NodeFunc[graph.State] {
	return func(_ context.Context, s graph.State) (graph.State, error) {
		out := graph.CopyState(s)
		out[key] = val
		return out, nil
	}
}

// TestRunEventStream_DeliversAgentEvents asserts that EventAgent* events
// emitted by an Agent node reach the per-call channel even when the graph
// has no graph-level GraphEventHook configured. This is the regression
// guard for the bridge gap fixed by threading the effective hook through
// context.
func TestRunEventStream_DeliversAgentEvents(t *testing.T) {
	sp := newScriptedProvider(&agent.ProviderResponse{Text: "answer"})
	a, err := agent.New(sp, prompt.Text("test"), nil)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	g, err := graph.New[graph.State]()
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	if _, err := g.Agent("inquire", a, graph.Keys("output", "input")); err != nil {
		t.Fatalf("g.Agent: %v", err)
	}
	g.Start("inquire")

	stream := g.RunEventStream(context.Background(), graph.State{"input": "hi"})

	counts := map[graph.EventType]int{}
	for ev := range stream.Events() {
		counts[ev.Type]++
	}
	if _, err := stream.Result(); err != nil {
		t.Fatalf("Result: %v", err)
	}

	// Required: at least one of each agent-level lifecycle event must have
	// reached the stream.
	for _, want := range []graph.EventType{
		graph.EventAgentModelStart,
		graph.EventAgentModelEnd,
		graph.EventAgentIterationStart,
		graph.EventAgentIterationEnd,
	} {
		if counts[want] == 0 {
			t.Errorf("expected at least one %s on the stream channel; got counts=%v",
				want, counts)
		}
	}

	// Sanity: graph-level events still arrive.
	if counts[graph.EventGraphStarted] == 0 {
		t.Error("missing EventGraphStarted")
	}
	if counts[graph.EventGraphCompleted] == 0 {
		t.Error("missing EventGraphCompleted")
	}
}

// TestResumeEventStream_DeliversResumedAndCompleted verifies that the
// EventResumed and EventGraphCompleted events fired by Resume reach the
// channel returned by ResumeEventStream — both used to bypass the per-call
// hook entirely.
func TestResumeEventStream_DeliversResumedAndCompleted(t *testing.T) {
	cp := memoryCheckpointer(t)

	g, err := graph.New[graph.State](graph.WithCheckpointer(cp))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	if _, err := g.Node("a", setterNode("a", "done"),
		graph.Out("a"), graph.In(),
	); err != nil {
		t.Fatalf("Node a: %v", err)
	}
	if _, err := g.Node("b", setterNode("b", "done"),
		graph.Out("b"), graph.In("a"),
	); err != nil {
		t.Fatalf("Node b: %v", err)
	}
	g.InterruptBefore("b")
	g.Start("a")

	threadID := "resume-stream-test"

	// Initial run hits the interrupt — drain so the run goroutine finishes.
	runStream := g.RunEventStream(context.Background(), graph.State{},
		graph.WithRunOption(graph.WithThreadID(threadID)),
	)
	for range runStream.Events() {
	}
	if _, err := runStream.Result(); err == nil {
		t.Fatal("expected interrupt error from initial run, got nil")
	}

	// Resume — the stream channel must surface EventResumed and the
	// terminal EventGraphCompleted.
	resumeStream := g.ResumeEventStream(context.Background(), threadID, nil)
	counts := map[graph.EventType]int{}
	for ev := range resumeStream.Events() {
		counts[ev.Type]++
	}
	if _, err := resumeStream.Result(); err != nil {
		t.Fatalf("resume Result: %v", err)
	}

	if counts[graph.EventResumed] == 0 {
		t.Errorf("expected EventResumed on resume stream, got counts=%v", counts)
	}
	if counts[graph.EventGraphCompleted] == 0 {
		t.Errorf("expected EventGraphCompleted on resume stream, got counts=%v", counts)
	}
}
