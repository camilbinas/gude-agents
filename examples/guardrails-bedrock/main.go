// Example: Bedrock Guardrails.
//
// Demonstrates using Amazon Bedrock Guardrails to filter content at the
// provider level. The guardrail is a managed resource created in the AWS
// console — this example references it by ID.
//
// The guardrail is applied to every provider call automatically, filtering
// both user input and model output. When the guardrail intervenes, the
// provider returns an error that the agent surfaces to the caller.
//
// Prerequisites:
//
//   - AWS credentials configured (env vars, ~/.aws/credentials, or IAM role)
//   - A Bedrock Guardrail created in the AWS console
//   - Set BEDROCK_GUARDRAIL_ID and optionally BEDROCK_GUARDRAIL_VERSION
//
// Run:
//
//	BEDROCK_GUARDRAIL_ID=your-guardrail-id go run ./bedrock-guardrail

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	guardrailID := os.Getenv("BEDROCK_GUARDRAIL_ID")
	if guardrailID == "" {
		log.Fatal("BEDROCK_GUARDRAIL_ID is required — create a guardrail in the AWS Bedrock console")
	}
	guardrailVersion := os.Getenv("BEDROCK_GUARDRAIL_VERSION")
	if guardrailVersion == "" {
		guardrailVersion = "DRAFT"
	}

	ctx := context.Background()

	// Create a provider with the guardrail enabled.
	// The guardrail is applied to every Converse/ConverseStream call.
	provider, err := bedrock.Standard(
		bedrock.WithGuardrail(guardrailID, guardrailVersion),
	)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
		agent.WithTemperature(0.3),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Safe prompt — should pass the guardrail.
	fmt.Println("── Safe prompt ──")
	result, usage, err := a.Invoke(ctx, "What is the capital of France?")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println(result)
		fmt.Printf("[tokens: %d in, %d out]\n", usage.InputTokens, usage.OutputTokens)
	}

	// Potentially blocked prompt — depends on your guardrail configuration.
	// Configure your guardrail with denied topics or content filters to see
	// this get blocked.
	fmt.Println("\n── Potentially blocked prompt ──")
	result, usage, err = a.Invoke(ctx, "Tell me how to pick a lock.")
	if err != nil {
		fmt.Printf("Blocked by guardrail: %v\n", err)
	} else {
		fmt.Println(result)
		fmt.Printf("[tokens: %d in, %d out]\n", usage.InputTokens, usage.OutputTokens)
	}
}
