// Example: Using MCP tools with a multi-step agent workflow.
//
// This example connects to the official MCP "everything" test server via stdio,
// discovers its tools, and runs an agent through a multi-step task that uses
// several tools in sequence: arithmetic, text manipulation, and a timed operation.
//
// The agent decides which tools to call and in what order — we just give it
// a goal and let it work.
//
// Prerequisites:
//   - Node.js / npx installed
//
// Run:
//
//	go run ./mcp-basic

package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/mcp"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	ctx := context.Background()

	// Connect to the MCP "everything" server — a test server that exercises
	// the full MCP protocol with tools like echo, add, longRunningOperation, etc.
	fmt.Println("Starting MCP server...")
	mcpClient, err := mcp.NewStdioClient(ctx,
		"npx", []string{"-y", "@modelcontextprotocol/server-everything"},
	)
	if err != nil {
		log.Fatalf("mcp connect: %v", err)
	}
	defer mcpClient.Close()

	// Discover all available tools.
	mcpTools, err := mcpClient.Tools(ctx)
	if err != nil {
		log.Fatalf("mcp tools: %v", err)
	}

	fmt.Printf("Discovered %d MCP tools:\n", len(mcpTools))
	for _, t := range mcpTools {
		fmt.Printf("  %-30s %s\n", t.Spec.Name, truncate(t.Spec.Description, 60))
	}
	fmt.Println()

	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.New(
		provider,
		prompt.Text(`You are a precise assistant that uses tools to complete tasks step by step.
When asked to perform calculations or operations, always use the available tools rather than computing yourself.
Show your work by describing what each tool returned.`),
		mcpTools,
		agent.WithMaxIterations(15),
	)
	if err != nil {
		log.Fatal(err)
	}

	// A multi-step task that requires the agent to chain several tool calls:
	// 1. Use the add tool for arithmetic
	// 2. Use echo to format a result message
	// 3. Summarize what was done
	task := `Complete these steps in order:
1. Use the get-sum tool to add 47 and 93
2. Echo the message "Result: <sum>" where <sum> is the answer from step 1
3. Summarize what you did and what each tool returned`

	fmt.Println("Running multi-step task...")
	fmt.Println(strings.Repeat("-", 60))

	start := time.Now()
	result, usage, err := a.Invoke(ctx, task)
	elapsed := time.Since(start)

	if err != nil {
		log.Fatalf("invoke: %v", err)
	}

	fmt.Println(result)
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("Completed in %s | tokens: %d in, %d out\n",
		elapsed.Round(time.Millisecond), usage.InputTokens, usage.OutputTokens)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
