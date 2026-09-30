// Example: Sentry integration with agent tracing.
//
// Prerequisites:
//   - A Sentry account with a Go project.
//   - Set SENTRY_DSN (find it in Project Settings > Client Keys).
//
// Run:
//
//	SENTRY_DSN=https://key@o123.ingest.us.sentry.io/456 go run ./tracing-sentry

package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tracing"
	sentrytrace "github.com/camilbinas/gude-agents/agent/tracing/sentry"
	"github.com/camilbinas/gude-agents/examples/utils"
)

func main() {
	ctx := context.Background()
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

	type weatherInput struct {
		City string `json:"city" description:"City name" required:"true"`
	}
	weatherTool := tool.New("get_weather", "Get the current weather for a city", func(_ context.Context, input weatherInput) (string, error) {
		if strings.EqualFold(input.City, "error-test") {
			return "", fmt.Errorf("weather service unavailable for: %s", input.City)
		}
		time.Sleep(time.Duration(50+rand.IntN(450)) * time.Millisecond)
		temp := 15 + rand.IntN(20)
		return fmt.Sprintf(`{"city": %q, "temp_c": %d, "condition": "partly cloudy"}`, input.City, temp), nil
	})

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a helpful assistant with access to a weather tool. Be concise.",
		agent.WithTools(weatherTool),
		agent.WithTemperature(0.3),
		agent.WithTopP(0.9),
		sentrytrace.WithSentry(tracing.WithContentCapture()),
		agent.WithMiddleware(
			sentrytrace.BreadcrumbMiddleware(),
			sentrytrace.ErrorCaptureMiddleware(),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Sentry-traced agent ready. Type 'quit' to exit.")
	fmt.Println("Try: What's the weather in Tokyo?")
	fmt.Println("Try: What's the weather in error-test?  (triggers error → Sentry Issue)")
	fmt.Println("Try: creative: write a haiku          (per-invocation temperature override → visible in trace)")
	fmt.Println()

	utils.Chat(agent.Background(), a, utils.ChatOptions{
		BeforeInvoke: func(c *agent.Context, input string) *agent.Context {
			if strings.HasPrefix(input, "creative:") {
				temp := 0.95
				return c.WithInferenceConfig(&agent.InferenceConfig{Temperature: &temp})
			}
			return nil
		},
		AfterInvoke: func(_ *agent.Context, result agent.Result, err error) {
			usage := result.Usage
			if err != nil {
				sentrytrace.CaptureAgentError(ctx, err, "", usage)
				log.Printf("Error (sent to Sentry): %v", err)
			}
			fmt.Printf("  [tokens: %d in, %d out]\n\n", usage.InputTokens, usage.OutputTokens)
		},
	})

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
