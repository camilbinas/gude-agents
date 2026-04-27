// Example: Colored debug logging for local development.
//
// Uses the agent/logging/debug package to emit human-readable, ANSI-colored
// log entries alongside the agent's streamed responses.
//
// Run:
//
//	go run ./logging-debug

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := context.Background()

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant with access to weather and time tools. Be concise."),
		[]tool.Tool{utils.WeatherTool(), utils.TimeTool()},
		debug.WithLogging(),
		agent.WithName("friendly-agent"),
		agent.WithConversation(conversation.NewInMemory(), "debug-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Debug logging agent ready. Type 'quit' to exit.")
	fmt.Println()
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")

	utils.Chat(ctx, a)
}
