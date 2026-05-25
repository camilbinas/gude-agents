package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

// TestIntegration_SystemPromptOverride verifies that
// Context.WithSystemPromptOverride takes precedence over the agent's
// configured instructions for a single invocation, and that subsequent
// invocations on the same agent return to the configured instructions.
//
// The override mechanism is the foundation for AgentCore A/B testing,
// where each request can route to a different system prompt without
// rebuilding the agent.
func TestIntegration_SystemPromptOverride(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	a, err := agent.New(p, prompt.Text(
		"You are a formal AI assistant. Always greet with 'Greetings, esteemed user.' Reply with that exact greeting line on every message.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Call 1: no override — should follow the formal default prompt.
	formalResult, err := a.Invoke(agent.NewContext(ctx), "Say hi")
	if err != nil {
		t.Fatalf("formal Invoke: %v", err)
	}
	t.Logf("formal: %s", formalResult)

	// Call 2: override with a casual prompt — should respond casually.
	casualCtx := agent.NewContext(ctx).WithSystemPromptOverride(
		"You are a casual AI buddy. Always greet with 'yo!' Reply with that exact greeting line on every message.",
	)
	casualResult, err := a.Invoke(casualCtx, "Say hi")
	if err != nil {
		t.Fatalf("casual Invoke: %v", err)
	}
	t.Logf("casual: %s", casualResult)

	// Call 3: no override again — back to formal.
	formalAgain, err := a.Invoke(agent.NewContext(ctx), "Say hi")
	if err != nil {
		t.Fatalf("formal-again Invoke: %v", err)
	}
	t.Logf("formal-again: %s", formalAgain)

	// Behavioral checks: the override should produce a different style than
	// the default. We use case-insensitive substring matching because LLMs
	// won't reproduce the seed text verbatim every time.
	formalLower := strings.ToLower(formalResult)
	casualLower := strings.ToLower(casualResult)
	formalAgainLower := strings.ToLower(formalAgain)

	if !strings.Contains(formalLower, "greetings") {
		t.Errorf("formal response should reflect default prompt; got: %s", formalResult)
	}
	if !strings.Contains(casualLower, "yo") {
		t.Errorf("casual response should reflect override; got: %s", casualResult)
	}
	if !strings.Contains(formalAgainLower, "greetings") {
		t.Errorf("formal-again response should revert to default after override expired; got: %s", formalAgain)
	}
}

// TestIntegration_SystemPromptOverride_PerRequestIsolation verifies that
// two concurrent invocations on the same agent with different overrides
// do not bleed into each other (the per-Context state is isolated).
func TestIntegration_SystemPromptOverride_PerRequestIsolation(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	a, err := agent.New(p, prompt.Text(
		"You are a default assistant.",
	), nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	type result struct {
		label string
		text  string
		err   error
	}
	results := make(chan result, 2)

	go func() {
		c := agent.NewContext(ctx).WithSystemPromptOverride(
			"You speak only in French. Reply with 'Bonjour!' to any greeting.",
		)
		text, err := a.Invoke(c, "hi")
		results <- result{label: "fr", text: text, err: err}
	}()

	go func() {
		c := agent.NewContext(ctx).WithSystemPromptOverride(
			"You speak only in German. Reply with 'Hallo!' to any greeting.",
		)
		text, err := a.Invoke(c, "hi")
		results <- result{label: "de", text: text, err: err}
	}()

	got := map[string]string{}
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("Invoke %s: %v", r.label, r.err)
		}
		got[r.label] = strings.ToLower(r.text)
		t.Logf("%s: %s", r.label, r.text)
	}

	// French response should NOT contain the German greeting and vice
	// versa. Loose check — LLMs may add extra words but should respect
	// the language directive.
	if !strings.Contains(got["fr"], "bonjour") {
		t.Errorf("french override leaked: %s", got["fr"])
	}
	if !strings.Contains(got["de"], "hallo") {
		t.Errorf("german override leaked: %s", got["de"])
	}
}
