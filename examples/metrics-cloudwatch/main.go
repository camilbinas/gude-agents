// Example: AWS CloudWatch metrics for agent invocations.
//
// Demonstrates how to enable CloudWatch metrics on an agent so that every
// invocation, iteration, provider call, and tool execution is published as
// CloudWatch custom metrics under a configurable namespace.
//
// Metrics are buffered in memory and flushed to CloudWatch periodically
// (default 60s, set to 15s here for demo purposes). On exit the shutdown
// function performs a final flush.
//
// Prerequisites:
//
//   - Valid AWS credentials (via environment, profile, or IAM role)
//   - cloudwatch:PutMetricData permission
//
// To run:
//
//	go run ./metrics-cloudwatch
//
// Then check the CloudWatch console under the "GudeAgents" namespace
// (or whatever you configure with WithNamespace).

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"

	cloudwatch "github.com/camilbinas/gude-agents/agent/metrics/cloudwatch"
)

func main() {
	ctx := context.Background()

	// Create the CloudWatch metrics option and shutdown function.
	withMetrics, shutdown := cloudwatch.WithMetrics(
		cloudwatch.WithNamespace("GudeAgents"),
		cloudwatch.WithFlushInterval(15*time.Second),
		cloudwatch.WithDimensions(map[string]string{
			"Environment": "development",
		}),
	)

	// Call shutdown on exit to flush remaining data points.
	defer func() {
		fmt.Println("Flushing CloudWatch metrics...")
		if err := shutdown(ctx); err != nil {
			log.Printf("cloudwatch shutdown: %v", err)
		}
	}()

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a helpful assistant with access to weather and time tools. Be concise."),
		[]tool.Tool{utils.WeatherTool(), utils.TimeTool()},
		agent.WithName("metrics-demo"),
		withMetrics,
	)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Interactive chat loop — metrics accumulate and flush every 15s.
	fmt.Println("CloudWatch metrics agent ready. Type 'quit' to exit.")
	fmt.Println("Metrics flush to CloudWatch every 15 seconds.")
	fmt.Println("Check the CloudWatch console under namespace 'GudeAgents'.")
	fmt.Println()
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()

	utils.Chat(ctx, a)
}
