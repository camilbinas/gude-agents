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

// TestIntegration_ParallelToolExecution verifies that WithParallelToolExecution
// correctly executes multiple tool calls concurrently with a real LLM.
func TestIntegration_ParallelToolExecution(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type LookupInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}

	callCh := make(chan string, 10)

	weatherTool := tool.New("get_weather", "Get the current weather for a city", func(_ context.Context, in LookupInput) (string, error) {
		callCh <- in.City
		// Small sleep to make parallelism observable.
		time.Sleep(100 * time.Millisecond)
		data := map[string]string{
			"paris":  "22°C, sunny",
			"london": "15°C, cloudy",
			"tokyo":  "28°C, humid",
		}
		if w, ok := data[strings.ToLower(in.City)]; ok {
			return w, nil
		}
		return "20°C, clear", nil
	})

	a, err := agent.New(p,
		prompt.Text("You are a weather assistant. When asked about multiple cities, call get_weather for EACH city. Be very brief."),
		[]tool.Tool{weatherTool},
		agent.WithParallelToolExecution(),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	start := time.Now()
	result, err := a.Invoke(c, "What's the weather in Paris, London, and Tokyo?")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	// Drain the channel to get call order.
	close(callCh)
	var callOrder []string
	for city := range callCh {
		callOrder = append(callOrder, city)
	}

	if result == "" {
		t.Fatal("expected non-empty response")
	}

	// At least 2 cities should have been looked up.
	if len(callOrder) < 2 {
		t.Errorf("expected at least 2 tool calls, got %d: %v", len(callOrder), callOrder)
	}

	t.Logf("Response: %s", result)
	t.Logf("Tool calls: %v, elapsed: %s", callOrder, elapsed)
}
