package integration_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

type scopeReadInput struct {
	Key string `json:"key" description:"Scope name to look up" required:"true"`
}

func TestIntegration_MultiScope_ToolReadsCorrectScope(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type call struct {
		key   string
		value string
	}
	var (
		mu    sync.Mutex
		calls []call
	)

	scopeTool := tool.New(
		"scope_for",
		"Look up the value of a scope key on the current request context.",
		func(ctx context.Context, in scopeReadInput) (string, error) {
			c := agent.FromContext(ctx)
			if c == nil {
				return "", fmt.Errorf("no agent context")
			}
			value, ok := c.Scope(in.Key)
			mu.Lock()
			calls = append(calls, call{key: in.Key, value: value})
			mu.Unlock()
			if !ok {
				return fmt.Sprintf("scope %q is not set", in.Key), nil
			}
			return fmt.Sprintf("scope %q = %s", in.Key, value), nil
		},
	)

	a, err := agent.New(
		p,
		`You are a tester. The user will ask you to look up a scope.
Use the scope_for tool with the named key, then return only the resolved value
(no explanation). If the tool reports a value, return it verbatim.`,
		agent.WithTools(scopeTool),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res1, err := a.Invoke(agent.NewContext(ctx).
		WithScope("project", "p-alpha").
		WithScope("user", "u-1"), "Look up scope 'project'.")
	if err != nil {
		t.Fatalf("invoke 1: %v", err)
	}
	t.Logf("invoke1 result: %s", res1.Text)

	res2, err := a.Invoke(agent.NewContext(ctx).
		WithScope("project", "p-beta").
		WithScope("user", "u-2"), "Look up scope 'project'.")
	if err != nil {
		t.Fatalf("invoke 2: %v", err)
	}
	t.Logf("invoke2 result: %s", res2.Text)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 2 {
		t.Fatalf("scope_for tool called %d times, expected at least 2", len(calls))
	}

	seenAlpha, seenBeta := false, false
	for _, call := range calls {
		if call.key != "project" {
			continue
		}
		switch call.value {
		case "p-alpha":
			seenAlpha = true
		case "p-beta":
			seenBeta = true
		default:
			t.Errorf("unexpected scope value for project: %q", call.value)
		}
	}
	if !seenAlpha {
		t.Error("first invocation did not see project=p-alpha")
	}
	if !seenBeta {
		t.Error("second invocation did not see project=p-beta")
	}
	if !strings.Contains(strings.ToLower(res1.Text), "p-alpha") {
		t.Errorf("response 1 should include resolved scope value; got: %s", res1.Text)
	}
	if !strings.Contains(strings.ToLower(res2.Text), "p-beta") {
		t.Errorf("response 2 should include resolved scope value; got: %s", res2.Text)
	}
}

// TestIntegration_MultiScope_ScopeFromFallback preserves the old test's intent
// while asserting the final strict contract: a missing scope never falls back
// to Identity, and Identity remains independently available.
func TestIntegration_MultiScope_ScopeFromFallback(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	var (
		capturedIdentity string
		capturedScope    string
		capturedScopeSet bool
	)
	scopeTool := tool.New(
		"ident_or_scope",
		"Look up an identity and a strict named scope.",
		func(ctx context.Context, in scopeReadInput) (string, error) {
			capturedScope, capturedScopeSet = agent.ScopeFrom(ctx, in.Key)
			capturedIdentity = agent.IdentityFrom(ctx)
			return capturedIdentity, nil
		},
	)

	a, err := agent.New(
		p,
		`Use the ident_or_scope tool with key="missing" and return the result.`,
		agent.WithTools(scopeTool),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx).WithIdentity("default-user")
	if _, err := a.Invoke(c, "Run the tool with key 'missing'."); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if capturedScopeSet || capturedScope != "" {
		t.Errorf("missing strict scope = (%q, %v), want (\"\", false)", capturedScope, capturedScopeSet)
	}
	if capturedIdentity != "default-user" {
		t.Errorf("IdentityFrom = %q, want %q", capturedIdentity, "default-user")
	}
}
