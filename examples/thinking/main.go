// Example: Extended thinking with live reasoning output.
//
// Shows how to enable extended thinking on a provider and use
// WithThinkingCallback to stream the model's internal reasoning
// to the user in real-time, alongside the final answer.
//
// Note: with extended thinking enabled, Claude tends to also explain
// its reasoning in the response text — this is intentional model behavior,
// not a bug. The thinking callback gives you the raw internal scratchpad;
// the response is Claude's visible summary of that reasoning.
//
// Run:
//
//	go run ./thinking

package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/provider/anthropic"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	provider, err := anthropic.New("claude-sonnet-4-6", anthropic.WithThinking(pvdr.ThinkingLow))
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a careful analytical thinker. Work through problems step by step."),
		nil,
		agent.WithThinkingCallback(func(chunk string) {
			fmt.Print(chunk)
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	question := "A bat and a ball cost $1.10 in total. The bat costs $1.00 more than the ball. How much does the ball cost?"

	fmt.Println("Question:", question)
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println("Thinking:")
	fmt.Println(strings.Repeat("─", 60))

	var once sync.Once
	usage, err := a.InvokeStream(ctx, question, func(chunk string) {
		once.Do(func() {
			fmt.Println()
			fmt.Println(strings.Repeat("─", 60))
			fmt.Println("Answer:")
			fmt.Println(strings.Repeat("─", 60))
		})
		fmt.Print(chunk)
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n\nTokens: %d in, %d out\n", usage.InputTokens, usage.OutputTokens)
}
