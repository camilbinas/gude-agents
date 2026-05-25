package integration_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// scopeReadOnly is an input type for the scope_for tool — it just takes a
// scope name to read.
type scopeReadInput struct {
	Key string `json:"key" description:"Scope name to look up" required:"true"`
}

// TestIntegration_MultiScope_ToolReadsCorrectScope verifies that
// Context.WithScope values are visible to tool handlers via
// agent.ScopeFrom and stay isolated per invocation.
//
// This is the integration test for the multi-scope memory feature
// (commit 8fc5f43): a tool reads a named scope value, the LLM is asked
// which scope to look up, and we verify the right scope value is
// returned.
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

	scopeTool := tool.New("scope_for",
		"Look up the value of a scope key on the current request context.",
		func(ctx context.Context, in scopeReadInput) (string, error) {
			c := agent.FromContext(ctx)
			if c == nil {
				return "", fmt.Errorf("no agent context")
			}
			v := c.Scope(in.Key)
			mu.Lock()
			calls = append(calls, call{key: in.Key, value: v})
			mu.Unlock()
			if v == "" {
				return fmt.Sprintf("scope %q is empty", in.Key), nil
			}
			return fmt.Sprintf("scope %q = %s", in.Key, v), nil
		},
	)

	a, err := agent.New(p,
		prompt.Text(`You are a tester. The user will ask you to look up a scope.
Use the scope_for tool with the named key, then return only the resolved value
(no explanation). If the tool reports a value, return it verbatim.`),
		[]tool.Tool{scopeTool},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Request 1: scope project=p-alpha
	c1 := agent.NewContext(ctx).
		WithScope("project", "p-alpha").
		WithScope("user", "u-1")
	res1, err := a.Invoke(c1, "Look up scope 'project'.")
	if err != nil {
		t.Fatalf("invoke 1: %v", err)
	}
	t.Logf("invoke1 result: %s", res1)

	// Request 2: same agent, different scopes — verify isolation.
	c2 := agent.NewContext(ctx).
		WithScope("project", "p-beta").
		WithScope("user", "u-2")
	res2, err := a.Invoke(c2, "Look up scope 'project'.")
	if err != nil {
		t.Fatalf("invoke 2: %v", err)
	}
	t.Logf("invoke2 result: %s", res2)

	// The tool should have been called at least twice — once per request.
	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 2 {
		t.Fatalf("scope_for tool called %d times, expected at least 2", len(calls))
	}

	// Filter calls for the "project" key. There must be at least one with
	// each value, in order, with no cross-contamination.
	seenAlpha, seenBeta := false, false
	for _, c := range calls {
		if c.key != "project" {
			continue
		}
		switch c.value {
		case "p-alpha":
			seenAlpha = true
		case "p-beta":
			seenBeta = true
		default:
			t.Errorf("unexpected scope value for project: %q", c.value)
		}
	}
	if !seenAlpha {
		t.Error("first invocation did not see project=p-alpha")
	}
	if !seenBeta {
		t.Error("second invocation did not see project=p-beta")
	}

	// Loose assertion on the LLM responses — the resolved values should
	// appear somewhere in the final text.
	if !strings.Contains(strings.ToLower(res1), "p-alpha") {
		t.Errorf("response 1 should include resolved scope value; got: %s", res1)
	}
	if !strings.Contains(strings.ToLower(res2), "p-beta") {
		t.Errorf("response 2 should include resolved scope value; got: %s", res2)
	}
}

// TestIntegration_MultiScope_ScopeFromFallback verifies ScopeFrom falls
// back to the Identifier when the named scope key is not set, both via
// direct API and through a tool reading the context.
func TestIntegration_MultiScope_ScopeFromFallback(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	var captured string
	scopeTool := tool.New("ident_or_scope",
		"Look up an identifier with optional scope fallback.",
		func(ctx context.Context, in scopeReadInput) (string, error) {
			captured = agent.ScopeFrom(ctx, in.Key)
			return captured, nil
		},
	)

	a, err := agent.New(p,
		prompt.Text(`Use the ident_or_scope tool with key="missing" and return the result.`),
		[]tool.Tool{scopeTool},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx).WithIdentifier("default-user")
	if _, err := a.Invoke(c, "Run the tool with key 'missing'."); err != nil {
		t.Fatalf("invoke: %v", err)
	}

	if captured != "default-user" {
		t.Errorf("ScopeFrom with missing key should fall back to Identifier; got %q", captured)
	}
}
