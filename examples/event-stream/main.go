// Example: Stream — consume every event from an agent run as a sequence of
// typed Event values. Useful for building UIs (SSE, WebSocket, CLI dashboards)
// while preserving live model output and detailed lifecycle events.
//
// Run:
//
//	go run ./event-stream

package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	a, err := agent.New(
		bedrock.Must(bedrock.GlobalClaudeSonnet4_6()),
		"You are concise.",
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background().WithDetailedEvents()
	var streamErr error
	for ev, err := range a.Stream(ctx, "Give me three fun facts about raccoons.") {
		switch ev.Type {
		case agent.EventStart:
			fmt.Println("── invoke started ──")

		case agent.EventIterationStart:
			if ev.Lifecycle != nil {
				fmt.Printf("[iter %d] start\n", ev.Lifecycle.Iteration)
			}

		case agent.EventModelStart:
			fmt.Println("  model: thinking…")

		case agent.EventText:
			if ev.Text != nil {
				fmt.Print(ev.Text.Content)
			}

		case agent.EventThinking:
			if ev.Thinking != nil {
				fmt.Print(ev.Thinking.Content)
			}

		case agent.EventToolStart:
			if ev.Tool != nil {
				fmt.Printf("\n  tool: %s start (%s)\n", ev.Tool.Name, ev.Tool.Input)
			}

		case agent.EventToolEnd:
			if ev.Tool == nil {
				break
			}
			if ev.Tool.Error != nil {
				fmt.Printf("  tool: %s err: %s\n", ev.Tool.Name, ev.Tool.Error.Message)
			} else {
				fmt.Printf("  tool: %s ok (%s) in %s\n", ev.Tool.Name, ev.Tool.Output, ev.Tool.Duration)
			}

		case agent.EventModelEnd:
			if ev.Lifecycle != nil {
				fmt.Printf("\n  model: stop=%s\n", ev.Lifecycle.StopReason)
			}

		case agent.EventIterationEnd:
			if ev.Lifecycle != nil {
				fmt.Printf("[iter %d] end (tools=%d, final=%v, %s)\n",
					ev.Lifecycle.Iteration, ev.Lifecycle.ToolCount, ev.Lifecycle.IsFinal, ev.Lifecycle.Duration)
			}

		case agent.EventMaxIterations:
			if ev.Lifecycle != nil {
				fmt.Printf("!! max iterations exceeded (limit=%d)\n", ev.Lifecycle.Limit)
			}

		case agent.EventEnd:
			fmt.Println("── invoke ended ──")
			if ev.Result != nil {
				fmt.Printf("usage: in=%d out=%d\n", ev.Result.Usage.InputTokens, ev.Result.Usage.OutputTokens)
			}
			if ev.Error != nil {
				fmt.Printf("invocation failed: %s\n", ev.Error.Message)
			}
		}
		if err != nil {
			streamErr = err
		}
	}
	if streamErr != nil {
		log.Fatal(streamErr)
	}
}
