// Example: Inference parameters (temperature, top_p, top_k, stop sequences).
//
// Shows how to control LLM sampling behavior at two levels:
//   - Agent-level: set defaults at construction time with WithTemperature, etc.
//   - Per-invocation: override for a single call via WithInferenceConfig on the context.
//
// The example creates one agent with a low temperature (deterministic) and then
// makes three calls: one with the agent defaults, one with a high-temperature
// override for creative output, and one with stop sequences to cut generation short.
//
// Run:
//
//	go run ./inference-params

package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func ptr[T any](v T) *T { return &v }

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	provider := bedrock.Must(bedrock.Standard())

	// ── Agent-level defaults ──────────────────────────────────────────
	// Low temperature for consistent, deterministic output.
	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Keep answers to 2-3 sentences."),
		nil,
		agent.WithTemperature(0.1),
		agent.WithTopP(0.9),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Call 1: Agent defaults (low temperature) ──────────────────────
	fmt.Println("── Call 1: Agent defaults (temperature=0.1) ──")
	result, usage, err := a.Invoke(ctx, "What is the theory of relativity?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
	fmt.Printf("[tokens: %d in / %d out]\n\n", usage.InputTokens, usage.OutputTokens)

	// ── Call 2: Per-invocation override (high temperature) ────────────
	// Override temperature for this single call to get more creative output.
	// TopP falls back to the agent-level value (0.9) since we don't override it.
	fmt.Println("── Call 2: Per-invocation override (temperature=0.95) ──")
	creativeCtx := agent.WithInferenceConfig(ctx, &agent.InferenceConfig{
		Temperature: ptr(0.95),
	})
	result, usage, err = a.Invoke(creativeCtx, "Write a haiku about Go programming.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
	fmt.Printf("[tokens: %d in / %d out]\n\n", usage.InputTokens, usage.OutputTokens)

	// ── Call 3: Stop sequences ────────────────────────────────────────
	// Use stop sequences to cut generation when the model outputs "2.".
	// This effectively limits the response to the first item in a list.
	fmt.Println("── Call 3: Stop sequences (stop at \"2.\") ──")
	stopCtx := agent.WithInferenceConfig(ctx, &agent.InferenceConfig{
		StopSequences: []string{"2."},
	})
	result, usage, err = a.Invoke(stopCtx, "List 5 benefits of unit testing.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(strings.TrimSpace(result))
	fmt.Printf("[tokens: %d in / %d out]\n", usage.InputTokens, usage.OutputTokens)
}
