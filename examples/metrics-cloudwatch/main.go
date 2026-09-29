// Example: AWS CloudWatch metrics for agent invocations.
//
// Demonstrates how to enable CloudWatch metrics on an agent so that every
// invocation, iteration, provider call, and tool execution is published as
// CloudWatch custom metrics under a configurable namespace.
//
// Prerequisites:
//   - Valid AWS credentials (via environment, profile, or IAM role)
//   - cloudwatch:PutMetricData permission
//
// To run:
//
//	go run ./metrics-cloudwatch

package main

import (
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	cloudwatch "github.com/camilbinas/gude-agents/agent/metrics/cloudwatch"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := agent.Background()
	withMetrics, shutdown := cloudwatch.WithMetrics(
		cloudwatch.WithNamespace("GudeAgents"),
		cloudwatch.WithFlushInterval(15*time.Second),
		cloudwatch.WithDimensions(map[string]string{
			"Environment": "development",
		}),
	)
	defer func() {
		fmt.Println("Flushing CloudWatch metrics...")
		if err := shutdown(ctx); err != nil {
			log.Printf("cloudwatch shutdown: %v", err)
		}
	}()

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a helpful assistant with access to weather and time tools. Be concise.",
		agent.WithTools(utils.WeatherTool(), utils.TimeTool()),
		agent.WithName("metrics-demo"),
		withMetrics,
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("CloudWatch metrics agent ready. Type 'quit' to exit.")
	fmt.Println("Metrics flush to CloudWatch every 15 seconds.")
	fmt.Println("Check the CloudWatch console under namespace 'GudeAgents'.")
	fmt.Println()
	fmt.Println("Try: What's the weather in Tokyo and the time in America/New_York?")
	fmt.Println()
	utils.Chat(ctx, a)
}
