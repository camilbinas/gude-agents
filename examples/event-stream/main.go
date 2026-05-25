// Example: InvokeEventStream — consume every event from an agent run as a
// channel of typed AgentEvent values. Useful for building UIs (SSE, WebSocket,
// CLI dashboards) without implementing the EventHook interface manually.
//
// Run:
//
//	go run ./event-stream

package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	a, err := agent.Default(
		bedrock.Must(bedrock.GlobalClaudeSonnet4_6()),
		prompt.Text("You are concise."),
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	events := a.InvokeEventStream(agent.Background(), "Give me three fun facts about raccoons.")

	for ev := range events {
		switch ev.Type {
		case agent.EventInvokeStart:
			fmt.Println("── invoke started ──")

		case agent.EventIterationStart:
			fmt.Printf("[iter %d] start\n", ev.Iteration)

		case agent.EventModelStart:
			fmt.Println("  model: thinking…")

		case agent.EventTextChunk:
			fmt.Print(ev.TextChunk)

		case agent.EventThinkingChunk:
			// Useful when extended thinking is enabled.
			fmt.Print(ev.ThinkingChunk)

		case agent.EventToolCallStart:
			fmt.Printf("\n  tool: %s start (%s)\n", ev.ToolName, ev.ToolInput)

		case agent.EventToolCallEnd:
			if ev.Err != nil {
				fmt.Printf("  tool: %s err: %v\n", ev.ToolName, ev.Err)
			} else {
				fmt.Printf("  tool: %s ok (%s) in %s\n", ev.ToolName, ev.ToolOutput, ev.Duration)
			}

		case agent.EventModelEnd:
			fmt.Printf("\n  model: stop=%s\n", ev.StopReason)

		case agent.EventIterationEnd:
			fmt.Printf("[iter %d] end (tools=%d, final=%v, %s)\n",
				ev.Iteration, ev.ToolCount, ev.IsFinal, ev.Duration)

		case agent.EventMaxIterations:
			fmt.Printf("!! max iterations exceeded (limit=%d)\n", ev.IterationLimit)

		case agent.EventInvokeEnd:
			fmt.Println("── invoke ended ──")
			fmt.Printf("usage: in=%d out=%d\n", ev.Usage.InputTokens, ev.Usage.OutputTokens)
			if ev.Err != nil {
				log.Fatalf("invocation failed: %v", ev.Err)
			}
		}
	}
}
