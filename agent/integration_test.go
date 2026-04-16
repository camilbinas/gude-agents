//go:build integration

package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/registry"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Integration tests that call real LLM APIs.
//
// Run with:
//   go test -tags=integration -v -timeout=120s ./agent/...
//
// Environment variables:
//   MODEL_PROVIDER  - "bedrock" (default), "openai", "anthropic"
//   MODEL_TIER      - "cheapest", "standard" (default), "smartest"
//
// To test multiple providers:
//   MODEL_PROVIDER=bedrock   go test -tags=integration -v -timeout=120s ./agent/...
//   MODEL_PROVIDER=openai    go test -tags=integration -v -timeout=120s ./agent/...
//   MODEL_PROVIDER=anthropic go test -tags=integration -v -timeout=120s ./agent/...

func newTestProvider(t *testing.T) agent.Provider {
	t.Helper()
	p, err := registry.FromEnv()
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	t.Logf("Using provider: MODEL_PROVIDER=%s MODEL_TIER=%s",
		envOr("MODEL_PROVIDER", "bedrock"), envOr("MODEL_TIER", "standard"))
	return &trackingProvider{inner: p}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func TestIntegration_SimpleTextResponse(t *testing.T) {
	p := newTestProvider(t)
	a, err := agent.New(p, prompt.Text("You are a helpful assistant. Be very brief."), nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "What is 2+2? Reply with just the number.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty response")
	}
	if !strings.Contains(result, "4") {
		t.Errorf("expected response to contain '4', got: %s", result)
	}
	t.Logf("Response: %s", result)
}

func TestIntegration_Streaming(t *testing.T) {
	p := newTestProvider(t)
	a, err := agent.New(p, prompt.Text("You are a helpful assistant. Be very brief."), nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var chunks []string
	_, err = a.InvokeStream(ctx, "Say hello in one word.", func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("InvokeStream error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one streamed chunk")
	}

	full := strings.Join(chunks, "")
	t.Logf("Streamed %d chunks, full response: %s", len(chunks), full)
}

func TestIntegration_StreamingWithMemory(t *testing.T) {
	p := newTestProvider(t)
	store := memory.NewStore()

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be very brief."),
		nil,
		agent.WithMemory(store, "stream-conv"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Turn 1: stream a response and establish context.
	var chunks1 []string
	_, err = a.InvokeStream(ctx, "My favorite number is 42. Remember that.", func(chunk string) {
		chunks1 = append(chunks1, chunk)
	})
	if err != nil {
		t.Fatalf("Turn 1 InvokeStream error: %v", err)
	}
	t.Logf("Turn 1 (%d chunks): %s", len(chunks1), strings.Join(chunks1, ""))

	// Turn 2: stream again and verify memory continuity.
	var chunks2 []string
	_, err = a.InvokeStream(ctx, "What is my favorite number?", func(chunk string) {
		chunks2 = append(chunks2, chunk)
	})
	if err != nil {
		t.Fatalf("Turn 2 InvokeStream error: %v", err)
	}
	full := strings.Join(chunks2, "")
	t.Logf("Turn 2 (%d chunks): %s", len(chunks2), full)

	if !strings.Contains(full, "42") {
		t.Errorf("expected response to contain '42', got: %s", full)
	}
}

func TestIntegration_ToolCalling(t *testing.T) {
	p := newTestProvider(t)

	type CalcInput struct {
		Expression string `json:"expression" description:"A math expression like 2+2" required:"true"`
	}

	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in CalcInput) (string, error) {
		if strings.Contains(in.Expression, "7") && strings.Contains(in.Expression, "6") {
			return "42", nil
		}
		return fmt.Sprintf("received: %s", in.Expression), nil
	})

	a, err := agent.New(p, prompt.Text("You are a calculator assistant. Always use the calculate tool for math. Be very brief."), []tool.Tool{calcTool})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "What is 7 times 6?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty response")
	}
	if !strings.Contains(result, "42") {
		t.Errorf("expected response to contain '42', got: %s", result)
	}
	t.Logf("Response: %s", result)
}

func TestIntegration_MultiToolCalls(t *testing.T) {
	p := newTestProvider(t)

	type LookupInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}

	weatherTool := tool.New("get_weather", "Get the current weather for a city", func(_ context.Context, in LookupInput) (string, error) {
		data := map[string]string{
			"paris":  "22°C, sunny",
			"london": "15°C, cloudy",
			"tokyo":  "28°C, humid",
		}
		if w, ok := data[strings.ToLower(in.City)]; ok {
			return w, nil
		}
		return "unknown city", nil
	})

	a, err := agent.New(p,
		prompt.Text("You are a weather assistant. Use the get_weather tool for each city the user asks about. Be very brief."),
		[]tool.Tool{weatherTool},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "What's the weather in Paris and London?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty response")
	}
	if !strings.Contains(result, "22") && !strings.Contains(strings.ToLower(result), "sunny") {
		t.Logf("Warning: response may not contain Paris weather: %s", result)
	}
	t.Logf("Response: %s", result)
}

func TestIntegration_MemoryMultiTurn(t *testing.T) {
	p := newTestProvider(t)
	store := memory.NewStore()

	a, err := agent.New(p,
		prompt.Text("You are a helpful assistant. Be very brief."),
		nil,
		agent.WithMemory(store, "test-conv"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, _, err = a.Invoke(ctx, "My favorite color is blue. Remember that.")
	if err != nil {
		t.Fatalf("first invoke error: %v", err)
	}

	result, _, err := a.Invoke(ctx, "What is my favorite color?")
	if err != nil {
		t.Fatalf("second invoke error: %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "blue") {
		t.Errorf("expected response to mention 'blue', got: %s", result)
	}
	t.Logf("Response: %s", result)
}

func TestIntegration_InvocationContext(t *testing.T) {
	p := newTestProvider(t)

	type EchoInput struct {
		Text string `json:"text" description:"Text to echo" required:"true"`
	}

	storeTool := tool.New("store_value", "Store a value for later use", func(ctx context.Context, in EchoInput) (string, error) {
		ic := agent.GetInvocationContext(ctx)
		if ic == nil {
			return "error: no invocation context", nil
		}
		ic.Set("stored", in.Text)
		return fmt.Sprintf("stored: %s", in.Text), nil
	})

	readTool := tool.NewRaw("read_value", "Read the previously stored value", map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}, func(ctx context.Context, _ json.RawMessage) (string, error) {
		ic := agent.GetInvocationContext(ctx)
		if ic == nil {
			return "error: no invocation context", nil
		}
		v, ok := ic.Get("stored")
		if !ok {
			return "nothing stored yet", nil
		}
		return fmt.Sprintf("read: %s", v), nil
	})

	a, err := agent.New(p,
		prompt.Text("You are a test assistant. When asked to store something, use store_value first, then use read_value to confirm. Be very brief."),
		[]tool.Tool{storeTool, readTool},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "Store the word 'banana' and then read it back.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "banana") {
		t.Errorf("expected response to mention 'banana', got: %s", result)
	}
	t.Logf("Response: %s", result)
}

func TestIntegration_InvokeStructured(t *testing.T) {
	p := newTestProvider(t)
	a, err := agent.New(p, prompt.Text("You are a helpful assistant that extracts structured data."), nil)
	if err != nil {
		t.Fatal(err)
	}

	type Person struct {
		Name string `json:"name" description:"The person's name" required:"true"`
		Age  int    `json:"age" description:"The person's age" required:"true"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, usage, err := agent.InvokeStructured[Person](ctx, a, "Extract the person: John is 30 years old.")
	if err != nil {
		t.Fatalf("InvokeStructured error: %v", err)
	}

	if result.Name == "" {
		t.Error("expected non-empty Name")
	}
	if !strings.EqualFold(result.Name, "John") {
		t.Errorf("expected Name to be 'John', got: %s", result.Name)
	}
	if result.Age != 30 {
		t.Errorf("expected Age to be 30, got: %d", result.Age)
	}
	t.Logf("Structured result: %+v, usage: %+v", result, usage)
}

func TestIntegration_TokenUsage(t *testing.T) {
	p := newTestProvider(t)
	a, err := agent.New(p, prompt.Text("You are a helpful assistant. Be very brief."), nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, usage, err := a.Invoke(ctx, "Say hello.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	if usage.InputTokens <= 0 {
		t.Errorf("expected InputTokens > 0, got: %d", usage.InputTokens)
	}
	if usage.OutputTokens <= 0 {
		t.Errorf("expected OutputTokens > 0, got: %d", usage.OutputTokens)
	}
	t.Logf("Token usage — input: %d, output: %d, total: %d", usage.InputTokens, usage.OutputTokens, usage.Total())
}

func TestIntegration_StreamingWithToolCalls(t *testing.T) {
	p := newTestProvider(t)

	type CalcInput struct {
		Expression string `json:"expression" description:"A math expression" required:"true"`
	}

	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in CalcInput) (string, error) {
		return "42", nil
	})

	a, err := agent.New(p,
		prompt.Text("You are a calculator. Always use the calculate tool. Be very brief."),
		[]tool.Tool{calcTool},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var chunks []string
	usage, err := a.InvokeStream(ctx, "What is 7 times 6?", func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("InvokeStream error: %v", err)
	}

	full := strings.Join(chunks, "")
	if !strings.Contains(full, "42") {
		t.Errorf("expected streamed response to contain '42', got: %s", full)
	}
	if usage.InputTokens <= 0 {
		t.Errorf("expected InputTokens > 0 after streaming with tools, got: %d", usage.InputTokens)
	}
	if usage.OutputTokens <= 0 {
		t.Errorf("expected OutputTokens > 0 after streaming with tools, got: %d", usage.OutputTokens)
	}
	t.Logf("Streamed %d chunks, response: %s, usage: input=%d output=%d",
		len(chunks), full, usage.InputTokens, usage.OutputTokens)
}

func TestIntegration_TokenUsageAccumulatesAcrossToolCalls(t *testing.T) {
	p := newTestProvider(t)

	type LookupInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}

	weatherTool := tool.New("get_weather", "Get weather for a city", func(_ context.Context, in LookupInput) (string, error) {
		return "20°C, clear", nil
	})

	a, err := agent.New(p,
		prompt.Text("You are a weather assistant. Use the get_weather tool for each city. Be very brief."),
		[]tool.Tool{weatherTool},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Ask about two cities to force multiple provider calls (tool call + final response).
	_, usage, err := a.Invoke(ctx, "What's the weather in Paris and Tokyo?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	// With tool calls, the agent makes at least 2 provider calls.
	// Accumulated usage should be higher than a single-call scenario.
	if usage.Total() <= 0 {
		t.Errorf("expected Total() > 0, got: %d", usage.Total())
	}
	// Input tokens should be substantial since the second call includes the full conversation.
	if usage.InputTokens < 20 {
		t.Errorf("expected accumulated InputTokens >= 20 (multi-call), got: %d", usage.InputTokens)
	}
	t.Logf("Accumulated usage across tool calls — input: %d, output: %d, total: %d",
		usage.InputTokens, usage.OutputTokens, usage.Total())
}

func TestIntegration_TokenBudgetEnforcement(t *testing.T) {
	p := newTestProvider(t)

	type CalcInput struct {
		Expression string `json:"expression" description:"A math expression" required:"true"`
	}

	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in CalcInput) (string, error) {
		return "42", nil
	})

	// Set a very small budget that will be exceeded after the first provider call.
	a, err := agent.New(p,
		prompt.Text("You are a calculator. Always use the calculate tool. Be very brief."),
		[]tool.Tool{calcTool},
		agent.WithTokenBudget(1), // 1 token budget — will be exceeded immediately
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, _, err = a.Invoke(ctx, "What is 2+2?")
	if err == nil {
		t.Fatal("expected ErrTokenBudgetExceeded, got nil")
	}
	if err != agent.ErrTokenBudgetExceeded {
		t.Errorf("expected ErrTokenBudgetExceeded, got: %v", err)
	}
	t.Logf("Budget enforcement worked: %v", err)
}

func TestIntegration_StreamingTokenUsage(t *testing.T) {
	p := newTestProvider(t)
	a, err := agent.New(p, prompt.Text("You are a helpful assistant. Be very brief."), nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	usage, err := a.InvokeStream(ctx, "Say hello.", func(_ string) {})
	if err != nil {
		t.Fatalf("InvokeStream error: %v", err)
	}

	if usage.InputTokens <= 0 {
		t.Errorf("expected InputTokens > 0 from streaming, got: %d", usage.InputTokens)
	}
	if usage.OutputTokens <= 0 {
		t.Errorf("expected OutputTokens > 0 from streaming, got: %d", usage.OutputTokens)
	}
	t.Logf("Streaming token usage — input: %d, output: %d, total: %d",
		usage.InputTokens, usage.OutputTokens, usage.Total())
}

func TestIntegration_ToolChoiceAny(t *testing.T) {
	p := newTestProvider(t)

	// Use Converse directly to test ToolChoice modes.
	resp, err := p.Converse(context.Background(), agent.ConverseParams{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello, how are you?"}}},
		},
		System: "You are a helpful assistant.",
		ToolConfig: []tool.Spec{
			{
				Name:        "greet",
				Description: "Generate a greeting",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"message": map[string]any{"type": "string", "description": "The greeting message"},
					},
					"required": []string{"message"},
				},
			},
		},
		ToolChoice: &tool.Choice{Mode: tool.ChoiceAny},
	})
	if err != nil {
		t.Fatalf("Converse with ToolChoiceAny error: %v", err)
	}

	// With ToolChoiceAny, the LLM must call some tool.
	if len(resp.ToolCalls) == 0 {
		t.Error("expected at least one tool call with ToolChoiceAny")
	}
	if len(resp.ToolCalls) > 0 {
		t.Logf("ToolChoiceAny: LLM called tool %q", resp.ToolCalls[0].Name)
	}
}
