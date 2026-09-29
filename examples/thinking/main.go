// Example: Extended thinking with live reasoning output.
//
// Shows how to enable extended thinking on a provider and consume the model's
// internal reasoning alongside the final answer using Agent.Stream.
// EventThinking and EventText values are interleaved on the same iterator.
//
// Note: with extended thinking enabled, Claude tends to also explain its
// reasoning in the response text — this is intentional model behavior, not a
// bug. Thinking events give you the raw internal scratchpad; text events are
// Claude's visible summary of that reasoning.
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

func main() {
	godotenv.Load() //nolint

	provider := anthropic.Must(anthropic.New("claude-sonnet-4-6", anthropic.WithThinking(pvdr.ThinkingLow)))

	a, err := agent.New(
		provider,
		prompt.Text("You are a careful analytical thinker. Work through problems step by step.").String(),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	question := "A bat and a ball cost $1.10 in total. The bat costs $1.00 more than the ball. How much does the ball cost?"

	fmt.Println("── reasoning ──")
	inThinking := false
	for ev, err := range a.Stream(agent.Background(), question) {
		if err != nil {
			log.Fatal(err)
		}
		switch ev.Type {
		case agent.EventThinking:
			if !inThinking {
				inThinking = true
			}
			fmt.Print(ev.Thinking.Content)

		case agent.EventText:
			if inThinking {
				fmt.Println("\n── answer ──")
				inThinking = false
			}
			fmt.Print(ev.Text.Content)

		case agent.EventEnd:
			fmt.Println()
		}
	}
}
