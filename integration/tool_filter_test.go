package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestIntegration_ToolFilter_HidesTool verifies that when a tool filter excludes
// a tool, the LLM cannot call it and must use the remaining tools instead.
func TestIntegration_ToolFilter_HidesTool(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type Input struct {
		Query string `json:"query" description:"Search query" required:"true"`
	}

	searchTool := tool.New("web_search", "Search the web for information", func(_ context.Context, in Input) (string, error) {
		return "web result: Go was created by Google", nil
	})

	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in Input) (string, error) {
		return "42", nil
	})

	// Filter that hides web_search — only calculate should be available.
	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Use available tools to answer questions. Be very brief."),
		[]tool.Tool{searchTool, calcTool},
		agent.WithToolFilter(func(_ *agent.Context, t tool.Tool) bool {
			return t.Spec.Name != "web_search"
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "What is 6 times 7? Use the calculate tool.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if !strings.Contains(result, "42") {
		t.Errorf("expected response to contain '42', got: %s", result)
	}
	t.Logf("Response: %s", result)
}

// TestIntegration_ToolFilter_ContextDriven verifies that tool filters can use
// context state to dynamically control tool availability.
func TestIntegration_ToolFilter_ContextDriven(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type Input struct {
		Text string `json:"text" description:"Text input" required:"true"`
	}

	adminTool := tool.New("delete_account", "Delete a user account permanently", func(_ context.Context, in Input) (string, error) {
		return "account deleted", nil
	})

	infoTool := tool.New("get_info", "Get general information", func(_ context.Context, in Input) (string, error) {
		return "Here is some general info about the system.", nil
	})

	// Filter: delete_account only available when "is_admin" is set on context.
	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Use available tools. Be very brief. If no suitable tool is available, say so."),
		[]tool.Tool{adminTool, infoTool},
		agent.WithToolFilter(func(c *agent.Context, t tool.Tool) bool {
			if t.Spec.Name == "delete_account" {
				v, ok := c.Get("is_admin")
				return ok && v.(bool)
			}
			return true
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "Get me some info about the system. Use the get_info tool.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty response")
	}
	t.Logf("Non-admin response: %s", result)
}
