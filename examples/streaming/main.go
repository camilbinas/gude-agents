// Run: go run ./streaming
//
// Stream delivers live text, reasoning, tool lifecycle, and a final result.
// The timeout is a real cancellation boundary: when it expires, the Agent
// stops work and the stream yields the cancellation error.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func main() {
	clock := tool.NewSimple("current_time", "Return the current UTC time", func(context.Context) (string, error) {
		return time.Now().UTC().Format(time.RFC3339), nil
	})
	a, err := agent.New(bedrock.Must(bedrock.Standard()), "You are concise. Use current_time when relevant.", agent.WithTools(clock))
	if err != nil {
		log.Fatal(err)
	}

	parent, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx := agent.NewContext(parent).WithDetailedEvents()
	for event, err := range a.Stream(ctx, "What time is it, and give one short productivity tip?") {
		if err != nil {
			log.Fatal(err)
		}
		switch event.Type {
		case agent.EventText:
			fmt.Print(event.Text.Content)
		case agent.EventThinking:
			fmt.Printf("\n[thinking] %s", event.Thinking.Content)
		case agent.EventToolStart:
			fmt.Printf("\n[tool start] %s\n", event.Tool.Name)
		case agent.EventToolEnd:
			fmt.Printf("[tool end] %s\n", event.Tool.Name)
		case agent.EventEnd:
			fmt.Printf("\n[done] tokens=%d\n", event.Result.Usage.Total())
		}
	}
}
