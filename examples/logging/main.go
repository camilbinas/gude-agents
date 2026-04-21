// Example: Structured logging for agent invocations.
//
// Demonstrates how to enable slog-based structured logging on an agent so
// that every invocation, iteration, provider call, and tool execution emits
// structured log entries with key-value attributes.
//
// Logs are written to tmp/agent.log so they don't interleave with the
// agent's streamed responses. Watch them in a second terminal:
//
//	tail -f tmp/agent.log | jq .
//
// To run:
//
//	go run ./logging
//
// Requires: go get github.com/camilbinas/gude-agents/agent/logging/slog

package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	agentslog "github.com/camilbinas/gude-agents/agent/logging/slog"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := context.Background()

	// 1. Open a log file so structured output doesn't mix with chat.
	if err := os.MkdirAll("tmp", 0o755); err != nil {
		log.Fatal(err)
	}
	logFile, err := os.Create("tmp/agent.log")
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()

	handler := slog.NewJSONHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelDebug, // show all events including Debug-level starts
	})

	// 2. Create a provider.
	provider := bedrock.Must(bedrock.Standard())

	// 3. Create the agent with structured logging and a couple of tools.
	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant with access to weather and time tools. Be concise."),
		[]tool.Tool{utils.WeatherTool(), utils.TimeTool()},
		agentslog.WithLogging(
			agentslog.WithHandler(handler),          // JSON output to file
			agentslog.WithMinLevel(slog.LevelDebug), // show all lifecycle events
		),
		agent.WithName("logging-demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Interactive chat loop — watch agent.log in another terminal.
	fmt.Println("Logging agent ready. Type 'quit' to exit.")
	fmt.Println("Logs → tmp/agent.log (try: tail -f tmp/agent.log | jq .)")
	fmt.Println()
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()

	utils.Chat(ctx, a, utils.ChatOptions{ShowUsage: true})
}
