// Example: Extended thinking with live reasoning output.
//
// Shows how to enable extended thinking on a provider and use
// EventHook.OnThinking to stream the model's internal reasoning
// to the user in real-time, alongside the final answer.
//
// Note: with extended thinking enabled, Claude tends to also explain
// its reasoning in the response text — this is intentional model behavior,
// not a bug. The EventHook gives you the raw internal scratchpad;
// the response is Claude's visible summary of that reasoning.
//
// Run:
//
//	go run ./thinking

package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/provider/anthropic"
	"github.com/joho/godotenv"
)

// thinkingHook prints thinking chunks to stdout.
type thinkingHook struct {
	agent.BaseEventHook
}

func (thinkingHook) OnThinking(_ *agent.Context, chunk string) { fmt.Print(chunk) }

func main() {
	godotenv.Load() //nolint

	provider := anthropic.Must(anthropic.New("claude-sonnet-4-6", anthropic.WithThinking(pvdr.ThinkingLow)))

	a, err := agent.Default(
		provider,
		prompt.Text("You are a careful analytical thinker. Work through problems step by step."),
		nil,
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	question := "A bat and a ball cost $1.10 in total. The bat costs $1.00 more than the ball. How much does the ball cost?"

	// Attach EventHook via context — scoped to this invocation.
	ctx := agent.Background().WithEventHook(thinkingHook{})

	err = a.InvokeStream(ctx, question, nil)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println()
}
