// Example: Extended thinking with live reasoning output.
//
// Shows how to enable extended thinking on a provider and consume the model's
// internal reasoning alongside the final answer using Agent.InvokeEventStream.
// Both EventThinkingChunk and EventTextChunk are interleaved on the same
// channel — no separate EventHook implementation needed.
//
// Note: with extended thinking enabled, Claude tends to also explain its
// reasoning in the response text — this is intentional model behavior, not a
// bug. The thinking_chunk events give you the raw internal scratchpad; the
// text_chunk events are Claude's visible summary of that reasoning.
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

	fmt.Println("── reasoning ──")
	inThinking := false
	for ev := range a.InvokeEventStream(agent.Background(), question) {
		switch ev.Type {
		case agent.EventThinkingChunk:
			if !inThinking {
				inThinking = true
			}
			fmt.Print(ev.ThinkingChunk)

		case agent.EventTextChunk:
			if inThinking {
				fmt.Println("\n── answer ──")
				inThinking = false
			}
			fmt.Print(ev.TextChunk)

		case agent.EventInvokeEnd:
			fmt.Println()
			if ev.Err != nil {
				log.Fatal(ev.Err)
			}
		}
	}
}
