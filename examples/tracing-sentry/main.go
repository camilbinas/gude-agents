// Example: Sentry integration with agent tracing.
//
// Demonstrates the full Sentry integration:
//   - OTLP traces exported to Sentry Performance (full span tree)
//   - Agent errors captured as Sentry Issues linked to the active trace
//   - Breadcrumbs for every tool call (visible in Issue detail)
//   - Error classification by type (provider, tool, guardrail)
//
// Prerequisites:
//
//   - A Sentry account with a Go project.
//   - Set SENTRY_DSN (find it in Project Settings > Client Keys).
//
// Run:
//
//	SENTRY_DSN=https://key@o123.ingest.us.sentry.io/456 \
//	go run ./tracing-sentry
//
// Then check:
//   - Sentry Performance → Traces for the full span tree
//   - Sentry Issues for captured errors with breadcrumb trail
//
// Try asking about "error-test" to trigger a tool error and see it in Sentry.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tracing"
	sentrytrace "github.com/camilbinas/gude-agents/agent/tracing/sentry"
)

func main() {
	ctx := context.Background()

	// 1. Set up Sentry — initializes SDK + OTLP trace export.
	//    Only the DSN is needed — the OTLP endpoint is derived automatically.
	shutdown, err := sentrytrace.Setup(ctx, sentrytrace.Config{
		DSN:         requireEnv("SENTRY_DSN"),
		Environment: envOr("SENTRY_ENVIRONMENT", "local"),
		ServiceName: "gude-agents-sentry-example",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	// 2. Create a provider.
	provider := bedrock.Must(bedrock.Standard())

	// 3. Define tools — "error-test" triggers an error for demo purposes.
	weatherTool := tool.NewRaw(
		"get_weather",
		"Get the current weather for a city",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "City name",
				},
			},
			"required": []any{"city"},
		},
		func(_ context.Context, input json.RawMessage) (string, error) {
			var req struct {
				City string `json:"city"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return "", err
			}
			if strings.EqualFold(req.City, "error-test") {
				return "", fmt.Errorf("weather service unavailable for: %s", req.City)
			}
			time.Sleep(time.Duration(50+rand.IntN(450)) * time.Millisecond)
			temp := 15 + rand.IntN(20)
			return fmt.Sprintf(`{"city": %q, "temp_c": %d, "condition": "partly cloudy"}`, req.City, temp), nil
		},
	)

	// 4. Create the agent with Sentry tracing + middleware.
	//    Inference parameters (temperature, top_p) are set at the agent level
	//    and will appear as span attributes on every agent.invoke trace in Sentry:
	//      gen_ai.request.temperature, gen_ai.request.top_p
	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant with access to a weather tool. Be concise."),
		[]tool.Tool{weatherTool},
		agent.WithParallelToolExecution(),
		agent.WithTemperature(0.3),
		agent.WithTopP(0.9),
		sentrytrace.WithSentry(tracing.WithContentCapture()), // captures prompts/responses — don't use in production
		agent.WithMiddleware(
			sentrytrace.BreadcrumbMiddleware(),
			sentrytrace.ErrorCaptureMiddleware(),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 5. Interactive loop.
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Sentry-traced agent ready. Type 'quit' to exit.")
	fmt.Println("Try: What's the weather in Tokyo?")
	fmt.Println("Try: What's the weather in error-test?  (triggers error → Sentry Issue)")
	fmt.Println("Try: creative: write a haiku          (per-invocation temperature override → visible in trace)")
	fmt.Println()

	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "quit") {
			break
		}

		// Per-invocation override: prefix "creative:" bumps temperature to 0.95.
		// The overridden value appears on the agent.invoke span in Sentry as
		// gen_ai.request.temperature=0.95 instead of the agent-level 0.3.
		callCtx := ctx
		if after, ok := strings.CutPrefix(input, "creative:"); ok {
			input = strings.TrimSpace(after)
			temp := 0.95
			callCtx = agent.WithInferenceConfig(ctx, &agent.InferenceConfig{
				Temperature: &temp,
			})
		}

		fmt.Print("Agent: ")
		usage, err := a.InvokeStream(callCtx, input, func(chunk string) {
			fmt.Print(chunk)
		})
		fmt.Println()

		if err != nil {
			// Capture invocation-level errors in Sentry with full context.
			sentrytrace.CaptureAgentError(ctx, err, input, usage)
			log.Printf("Error (sent to Sentry): %v", err)
		}

		fmt.Printf("  [tokens: %d in, %d out]\n\n", usage.InputTokens, usage.OutputTokens)
	}

	fmt.Println("Flushing to Sentry...")
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is required", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
