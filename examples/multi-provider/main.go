// Example: Multi-provider routing.
//
// Simple tasks are routed to Bedrock (cheap, fast), complex tasks to OpenAI
// (more capable). Both implement agent.Provider so the routing logic is just
// a conditional at the call site — no framework magic needed.
//
// Key concepts demonstrated:
//   - Routing across different cloud providers (Bedrock + OpenAI)
//   - ModelIdentifier interface to read the model ID at runtime
//   - Comparing latency and token usage across providers
//
// Prerequisites:
//   - AWS credentials configured
//   - OPENAI_API_KEY env var set
//
// Run:
//
// go run ./examples/multi-provider
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/provider/openai"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()
	instructions := prompt.Text("You are a helpful assistant. Be concise.")

	// Bedrock provider with cheapest model
	cheap, err := bedrock.Cheapest()
	if err != nil {
		log.Fatal(err)
	}

	// OpenAI provider with most capable model
	smart, err := openai.Smartest()
	if err != nil {
		log.Fatal(err)
	}

	cheapAgent, err := agent.Default(cheap, instructions, nil)
	if err != nil {
		log.Fatal(err)
	}

	smartAgent, err := agent.Default(smart, instructions, nil)
	if err != nil {
		log.Fatal(err)
	}

	tasks := []struct {
		label    string
		question string
		complex  bool // true → OpenAI, false → Bedrock
	}{
		{
			label:    "simple",
			question: "What is the capital of France?",
			complex:  false,
		},
		{
			label:    "simple",
			question: "Convert 100 USD to EUR at a rate of 0.92.",
			complex:  false,
		},
		{
			label:    "complex",
			question: "Explain the trade-offs between microservices and a monolith for a startup with 3 engineers.",
			complex:  true,
		},
		{
			label:    "complex",
			question: "Write a short Go function that retries an operation up to 3 times with exponential backoff.",
			complex:  true,
		},
	}

	for _, task := range tasks {
		a := cheapAgent
		modelLabel := cheap.ModelId()
		if task.complex {
			a = smartAgent
			modelLabel = smart.ModelId()
		}

		fmt.Printf("── [%s] → %s ──\n", task.label, modelLabel)
		fmt.Printf("Q: %s\n", task.question)

		start := time.Now()
		result, usage, err := a.Invoke(ctx, task.question)
		elapsed := time.Since(start)

		if err != nil {
			log.Printf("error: %v\n\n", err)
			continue
		}

		fmt.Printf("A: %s\n", result)
		fmt.Printf("   [%d in / %d out tokens, %s]\n\n", usage.InputTokens, usage.OutputTokens, elapsed.Round(time.Millisecond))
	}
}
