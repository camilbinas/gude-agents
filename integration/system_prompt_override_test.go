package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// TestIntegration_SystemPromptOverride verifies that Context.WithInstructions
// takes precedence for one invocation and does not mutate the Agent config.
func TestIntegration_SystemPromptOverride(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	a, err := agent.New(p, "You are a formal AI assistant. Always greet with 'Greetings, esteemed user.' Reply with that exact greeting line on every message.")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	formalResult, err := a.Invoke(agent.NewContext(ctx), "Say hi")
	if err != nil {
		t.Fatalf("formal Invoke: %v", err)
	}
	t.Logf("formal: %s", formalResult.Text)

	casualResult, err := a.Invoke(
		agent.NewContext(ctx).WithInstructions("You are a casual AI buddy. Always greet with 'yo!' Reply with that exact greeting line on every message."),
		"Say hi",
	)
	if err != nil {
		t.Fatalf("casual Invoke: %v", err)
	}
	t.Logf("casual: %s", casualResult.Text)

	formalAgain, err := a.Invoke(agent.NewContext(ctx), "Say hi")
	if err != nil {
		t.Fatalf("formal-again Invoke: %v", err)
	}
	t.Logf("formal-again: %s", formalAgain.Text)

	formalLower := strings.ToLower(formalResult.Text)
	casualLower := strings.ToLower(casualResult.Text)
	formalAgainLower := strings.ToLower(formalAgain.Text)
	if !strings.Contains(formalLower, "greetings") {
		t.Errorf("formal response should reflect default prompt; got: %s", formalResult.Text)
	}
	if !strings.Contains(casualLower, "yo") {
		t.Errorf("casual response should reflect override; got: %s", casualResult.Text)
	}
	if !strings.Contains(formalAgainLower, "greetings") {
		t.Errorf("formal-again response should revert to default after override expired; got: %s", formalAgain.Text)
	}
}

func TestIntegration_SystemPromptOverride_PerRequestIsolation(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	a, err := agent.New(p, "You are a default assistant.")
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
		run, runErr := a.Invoke(
			agent.NewContext(ctx).WithInstructions("You speak only in French. Reply with 'Bonjour!' to any greeting."),
			"hi",
		)
		results <- result{label: "fr", text: run.Text, err: runErr}
	}()

	go func() {
		run, runErr := a.Invoke(
			agent.NewContext(ctx).WithInstructions("You speak only in German. Reply with 'Hallo!' to any greeting."),
			"hi",
		)
		results <- result{label: "de", text: run.Text, err: runErr}
	}()

	got := map[string]string{}
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatalf("Invoke %s: %v", r.label, r.err)
		}
		got[r.label] = strings.ToLower(r.text)
		t.Logf("%s: %s", r.label, r.text)
	}
	if !strings.Contains(got["fr"], "bonjour") {
		t.Errorf("french override leaked: %s", got["fr"])
	}
	if !strings.Contains(got["de"], "hallo") {
		t.Errorf("german override leaked: %s", got["de"])
	}
}
