package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/structured"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Integration tests that call real LLM APIs.
//
// Run with:
//   go test -v -timeout=120s ./...
//
// Environment variables:
//   MODEL_PROVIDER  - Provider name (default: bedrock)
//   MODEL_TIER      - Provider tier (default: standard)
//   AWS_REGION      - AWS region (default: eu-central-1)

func TestIntegration_SimpleTextResponse(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	a, err := agent.New(p, string("You are a helpful assistant. Be very brief."))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "What is 2+2? Reply with just the number.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty response")
	}
	if !strings.Contains(result.Text, "4") {
		t.Errorf("expected response to contain '4', got: %s", result.Text)
	}
	t.Logf("Response: %s", result.Text)
}

func TestIntegration_Streaming(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	a, err := agent.New(p, string("You are a helpful assistant. Be very brief."))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	var chunks []string
	for chunk, streamErr := range a.TextStream(c, "Say hello in one word.") {
		if streamErr != nil {
			t.Fatalf("TextStream error: %v", streamErr)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one streamed chunk")
	}

	full := strings.Join(chunks, "")
	t.Logf("Streamed %d chunks, full response: %s", len(chunks), full)
}

func TestIntegration_StreamingWithMemory(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	a, err := agent.New(p,
		string("You are a helpful assistant. Be very brief."),
		agent.WithConversationStore(store),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx).WithConversationID("stream-conv")

	// Turn 1: stream a response and establish context.
	var chunks1 []string
	for chunk, streamErr := range a.TextStream(c, "My favorite number is 42. Remember that.") {
		if streamErr != nil {
			t.Fatalf("Turn 1 TextStream error: %v", streamErr)
		}
		chunks1 = append(chunks1, chunk)
	}
	t.Logf("Turn 1 (%d chunks): %s", len(chunks1), strings.Join(chunks1, ""))

	// Turn 2: stream again and verify memory continuity.
	var chunks2 []string
	for chunk, streamErr := range a.TextStream(c, "What is my favorite number?") {
		if streamErr != nil {
			t.Fatalf("Turn 2 TextStream error: %v", streamErr)
		}
		chunks2 = append(chunks2, chunk)
	}
	full := strings.Join(chunks2, "")
	t.Logf("Turn 2 (%d chunks): %s", len(chunks2), full)

	if !strings.Contains(full, "42") {
		t.Errorf("expected response to contain '42', got: %s", full)
	}
}

func TestIntegration_ToolCalling(t *testing.T) {
	t.Parallel()
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

	a, err := agent.New(p, string("You are a calculator assistant. Always use the calculate tool for math. Be very brief."), agent.WithTools(calcTool))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "What is 7 times 6?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty response")
	}
	if !strings.Contains(result.Text, "42") {
		t.Errorf("expected response to contain '42', got: %s", result.Text)
	}
	t.Logf("Response: %s", result.Text)
}

func TestIntegration_MultiToolCalls(t *testing.T) {
	t.Parallel()
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
		string("You are a weather assistant. Use the get_weather tool for each city the user asks about. Be very brief."),
		agent.WithTools(weatherTool),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "What's the weather in Paris and London?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty response")
	}
	if !strings.Contains(result.Text, "22") && !strings.Contains(strings.ToLower(result.Text), "sunny") {
		t.Logf("Warning: response may not contain Paris weather: %s", result.Text)
	}
	t.Logf("Response: %s", result.Text)
}

func TestIntegration_MemoryMultiTurn(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	a, err := agent.New(p,
		string("You are a helpful assistant. Be very brief."),
		agent.WithConversationStore(store),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx).WithConversationID("test-conv")
	_, err = a.Invoke(c, "My favorite color is blue. Remember that.")
	if err != nil {
		t.Fatalf("first invoke error: %v", err)
	}

	result, err := a.Invoke(c, "What is my favorite color?")
	if err != nil {
		t.Fatalf("second invoke error: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Text), "blue") {
		t.Errorf("expected response to mention 'blue', got: %s", result.Text)
	}
	t.Logf("Response: %s", result.Text)
}

func TestIntegration_InvocationContext(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type EchoInput struct {
		Text string `json:"text" description:"Text to echo" required:"true"`
	}

	storeTool := tool.New("store_value", "Store a value for later use", func(ctx context.Context, in EchoInput) (string, error) {
		c := agent.FromContext(ctx)
		if c == nil {
			return "error: no invocation context", nil
		}
		c.Set("stored", in.Text)
		return fmt.Sprintf("stored: %s", in.Text), nil
	})

	readTool := tool.NewRaw("read_value", "Read the previously stored value", func(ctx context.Context, _ json.RawMessage) (string, error) {
		c := agent.FromContext(ctx)
		if c == nil {
			return "error: no invocation context", nil
		}
		v, ok := c.Get("stored")
		if !ok {
			return "nothing stored yet", nil
		}
		return fmt.Sprintf("read: %s", v), nil
	}, tool.WithSchema(map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}))

	a, err := agent.New(p,
		string("You are a test assistant. When asked to store something, use store_value first, then use read_value to confirm. Be very brief."),
		agent.WithTools(storeTool, readTool),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "Store the word 'banana' and then read it back.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Text), "banana") {
		t.Errorf("expected response to mention 'banana', got: %s", result.Text)
	}
	t.Logf("Response: %s", result.Text)
}

func TestIntegration_InvokeStructured(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	a, err := agent.New(p, string("You are a helpful assistant that extracts structured data."))
	if err != nil {
		t.Fatal(err)
	}

	type Person struct {
		Name string `json:"name" description:"The person's name" required:"true"`
		Age  int    `json:"age" description:"The person's age" required:"true"`
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := structured.Invoke[Person](c, a, "Extract the person: John is 30 years old.")
	if err != nil {
		t.Fatalf("structured.Invoke error: %v", err)
	}

	if result.Value.Name == "" {
		t.Error("expected non-empty Name")
	}
	if !strings.EqualFold(result.Value.Name, "John") {
		t.Errorf("expected Name to be 'John', got: %s", result.Value.Name)
	}
	if result.Value.Age != 30 {
		t.Errorf("expected Age to be 30, got: %d", result.Value.Age)
	}
	t.Logf("Structured result: %+v, usage: %+v", result.Value, result.Run.Usage)
}

func TestIntegration_TokenUsage(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	a, err := agent.New(p, string("You are a helpful assistant. Be very brief."))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "Say hello.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	usage := result.Usage
	if usage.InputTokens <= 0 {
		t.Errorf("expected InputTokens > 0, got: %d", usage.InputTokens)
	}
	if usage.OutputTokens <= 0 {
		t.Errorf("expected OutputTokens > 0, got: %d", usage.OutputTokens)
	}
	t.Logf("Token usage — input: %d, output: %d, total: %d", usage.InputTokens, usage.OutputTokens, usage.Total())
}

func TestIntegration_StreamingWithToolCalls(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type CalcInput struct {
		Expression string `json:"expression" description:"A math expression" required:"true"`
	}

	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in CalcInput) (string, error) {
		return "42", nil
	})

	a, err := agent.New(p,
		string("You are a calculator. Always use the calculate tool. Be very brief."),
		agent.WithTools(calcTool),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	var chunks []string
	var runResult *agent.Result
	for event, streamErr := range a.Stream(c, "What is 7 times 6?") {
		if streamErr != nil {
			t.Fatalf("Stream error: %v", streamErr)
		}
		switch event.Type {
		case agent.EventText:
			chunks = append(chunks, event.Text.Content)
		case agent.EventEnd:
			runResult = event.Result
		}
	}
	if runResult == nil {
		t.Fatal("stream ended without a result")
	}

	usage := runResult.Usage
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
	t.Parallel()
	p := newTestProvider(t)

	type LookupInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}

	weatherTool := tool.New("get_weather", "Get weather for a city", func(_ context.Context, in LookupInput) (string, error) {
		return "20°C, clear", nil
	})

	a, err := agent.New(p,
		string("You are a weather assistant. Use the get_weather tool for each city. Be very brief."),
		agent.WithTools(weatherTool),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)

	// Ask about two cities to force multiple provider calls (tool call + final response).
	result, err := a.Invoke(c, "What's the weather in Paris and Tokyo?")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	usage := result.Usage
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
	t.Parallel()
	p := newTestProvider(t)

	type CalcInput struct {
		Expression string `json:"expression" description:"A math expression" required:"true"`
	}

	calcTool := tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in CalcInput) (string, error) {
		return "42", nil
	})

	// Set a very small budget that will be exceeded after the first provider call.
	a, err := agent.New(p,
		string("You are a calculator. Always use the calculate tool. Be very brief."),
		agent.WithTools(calcTool),
		agent.WithTokenBudget(1), // 1 token budget — will be exceeded immediately
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	_, err = a.Invoke(c, "What is 2+2?")
	if err == nil {
		t.Fatal("expected ErrTokenBudgetExceeded, got nil")
	}
	if err != agent.ErrTokenBudgetExceeded {
		t.Errorf("expected ErrTokenBudgetExceeded, got: %v", err)
	}
	t.Logf("Budget enforcement worked: %v", err)
}

func TestIntegration_StreamingTokenUsage(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	a, err := agent.New(p, string("You are a helpful assistant. Be very brief."))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	var runResult *agent.Result
	for event, streamErr := range a.Stream(c, "Say hello.") {
		if streamErr != nil {
			t.Fatalf("Stream error: %v", streamErr)
		}
		if event.Type == agent.EventEnd {
			runResult = event.Result
		}
	}
	if runResult == nil {
		t.Fatal("stream ended without a result")
	}

	usage := runResult.Usage
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
	t.Parallel()
	p := newTestProvider(t)

	// Use Provider.Stream directly to test ToolChoice modes.
	resp, err := p.Stream(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "Hello, how are you?"}}},
		},
		System: "You are a helpful assistant.",
		Tools: []tool.Spec{
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
	}, nil)
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

func TestIntegration_InferenceConfig_AgentLevel(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	// Low temperature should produce consistent, deterministic output.
	a, err := agent.New(p,
		string("You are a helpful assistant. Be very brief. Reply with exactly one word."),
		agent.WithTemperature(0.0),
		agent.WithMaxOutputTokens(10),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	result, err := a.Invoke(c, "What is the capital of France? Reply with just the city name.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if !strings.Contains(result.Text, "Paris") {
		t.Errorf("expected response to contain 'Paris', got: %s", result.Text)
	}
	usage := result.Usage
	if usage.InputTokens <= 0 || usage.OutputTokens <= 0 {
		t.Errorf("expected non-zero token usage, got: %+v", usage)
	}
	t.Logf("Response: %s (tokens: %d in, %d out)", result.Text, usage.InputTokens, usage.OutputTokens)
}

func TestIntegration_InferenceConfig_PerInvocationOverride(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	// Agent-level: low temperature.
	a, err := agent.New(p,
		string("You are a helpful assistant. Be very brief."),
		agent.WithTemperature(0.0),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Per-invocation override: high temperature for creative output.
	temp := 0.9
	c := agent.NewContext(ctx).WithInferenceConfig(&agent.InferenceConfig{
		Temperature: &temp,
	})

	result, err := a.Invoke(c, "Write a one-sentence haiku about clouds.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected non-empty response")
	}
	t.Logf("Creative response (temp=0.9): %s", result.Text)
}

func TestIntegration_InferenceConfig_StopSequences(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	a, err := agent.New(p,
		string("You are a helpful assistant. When listing items, number them as 1. 2. 3. etc."),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use stop sequences to cut generation after the first item.
	c := agent.NewContext(ctx).WithInferenceConfig(&agent.InferenceConfig{
		StopSequences: []string{"2."},
	})

	result, err := a.Invoke(c, "List 5 programming languages.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	// The response should be cut short — it should NOT contain "3." or "4."
	if strings.Contains(result.Text, "3.") {
		t.Errorf("expected stop sequence to cut generation before '3.', got: %s", result.Text)
	}
	t.Logf("Stopped response: %s", strings.TrimSpace(result.Text))
}

func TestIntegration_InferenceConfig_InvalidPerInvocationReturnsError(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	a, err := agent.New(p,
		string("You are a helpful assistant."),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Invalid temperature should fail before calling the provider.
	badTemp := 5.0
	c := agent.NewContext(ctx).WithInferenceConfig(&agent.InferenceConfig{
		Temperature: &badTemp,
	})

	_, err = a.Invoke(c, "hello")
	if err == nil {
		t.Fatal("expected error for invalid per-invocation temperature, got nil")
	}
	if !strings.Contains(err.Error(), "inference config") {
		t.Errorf("expected error to mention 'inference config', got: %v", err)
	}
	t.Logf("Correctly rejected invalid config: %v", err)
}
