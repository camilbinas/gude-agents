package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

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

	a, err := agent.New(
		p,
		"You are a helpful assistant. Use available tools to answer questions. Be very brief.",
		agent.WithTools(searchTool, calcTool),
		agent.WithToolFilter(func(_ *agent.Context, tool tool.Tool) bool {
			return tool.Spec.Name != "web_search"
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := a.Invoke(agent.NewContext(ctx), "What is 6 times 7? Use the calculate tool.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if !strings.Contains(result.Text, "42") {
		t.Errorf("expected response to contain '42', got: %s", result.Text)
	}
	t.Logf("Response: %s", result.Text)
}

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

	a, err := agent.New(
		p,
		"You are a helpful assistant. Use available tools. Be very brief. If no suitable tool is available, say so.",
		agent.WithTools(adminTool, infoTool),
		agent.WithToolFilter(func(c *agent.Context, tool tool.Tool) bool {
			if tool.Spec.Name == "delete_account" {
				value, ok := c.Get("is_admin")
				isAdmin, valid := value.(bool)
				return ok && valid && isAdmin
			}
			return true
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := a.Invoke(agent.NewContext(ctx), "Get me some info about the system. Use the get_info tool.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty response")
	}
	t.Logf("Non-admin response: %s", result.Text)
}
