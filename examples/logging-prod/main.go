// Example: Structured JSON logging for production.
//
// Uses the agent/logging/slog package to emit structured JSON log entries to
// tmp/agent.log. Logs are written to a file so they don't interleave with the
// agent's streamed responses. Suitable for log aggregation (Datadog, CloudWatch
// Logs, Loki, etc.).
//
// Watch logs in a second terminal while chatting:
//
//	tail -f tmp/agent.log | jq .
//
// To run:
//
//	go run ./logging-prod
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

	// Write structured JSON logs to a file so they don't mix with chat output.
	if err := os.MkdirAll("tmp", 0o755); err != nil {
		log.Fatal(err)
	}
	logFile, err := os.Create("tmp/agent.log")
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant with access to weather and time tools. Be concise."),
		[]tool.Tool{utils.WeatherTool(), utils.TimeTool()},
		agentslog.WithLogging(
			agentslog.WithHandler(slog.NewJSONHandler(logFile, &slog.HandlerOptions{
				Level: slog.LevelInfo, // Info+ only — skip noisy Debug start events
			})),
		),
		agent.WithName("logging-demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Production logging agent ready. Type 'quit' to exit.")
	fmt.Println("Structured JSON logs → tmp/agent.log")
	fmt.Println("Watch: tail -f tmp/agent.log | jq .")
	fmt.Println()
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()

	utils.Chat(ctx, a)
}
