// Example: Streaming agent events over SSE with Fiber v3.
//
// Demonstrates how to use Agent.InvokeEventStream with a Fiber HTTP handler
// to stream tool calls, model lifecycle, thinking, and text chunks to the
// browser in real-time. Reading a single channel of typed AgentEvents replaces
// implementing EventHook plus a separate StreamCallback.
//
// The agent is created once at startup and shared across requests. Each
// request gets its own event channel — no concurrency issues.
//
// Run:
//
//	go run ./fiber-sse
//
// Test with curl:
//
//	curl -N "http://localhost:3000/chat?q=what+is+the+capital+of+france"

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/gofiber/fiber/v3"
	"github.com/joho/godotenv"
)

// sseEmit writes a single SSE event with a JSON-encoded payload.
func sseEmit(w *bufio.Writer, event string, data any) {
	payload, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\tdata: %s\n", event, payload)
	w.Flush()
}

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.GlobalClaudeSonnet4_6(bedrock.WithThinking(pvdr.ThinkingLow)))

	// Shared agent — created once, used by all requests.
	a, err := agent.New(provider,
		prompt.Text("You are a helpful assistant with access to tools."),
		[]tool.Tool{
			tool.NewRaw("get_weather", "Get current weather for a city",
				map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string", "description": "City name"},
					},
					"required": []string{"city"},
				},
				func(_ context.Context, input json.RawMessage) (string, error) {
					var params struct{ City string }
					json.Unmarshal(input, &params)
					time.Sleep(100 * time.Millisecond) // simulate latency
					return fmt.Sprintf(`{"city":"%s","temp":"22°C","condition":"sunny"}`, params.City), nil
				},
			),
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	app := fiber.New()

	app.Get("/chat", func(c fiber.Ctx) error {
		q := c.Query("q")
		if q == "" {
			return c.Status(400).SendString("missing ?q= parameter")
		}

		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")

		return c.SendStreamWriter(func(w *bufio.Writer) {
			ctx := agent.NewContext(c.Context())

			for ev := range a.InvokeEventStream(ctx, q) {
				switch ev.Type {
				case agent.EventTextChunk:
					sseEmit(w, "text", map[string]string{"chunk": ev.TextChunk})

				case agent.EventThinkingChunk:
					sseEmit(w, "thinking", map[string]string{"chunk": ev.ThinkingChunk})

				case agent.EventToolCallStart:
					sseEmit(w, "tool_start", map[string]any{
						"tool":  ev.ToolName,
						"input": ev.ToolInput,
					})

				case agent.EventToolCallEnd:
					data := map[string]any{
						"tool":        ev.ToolName,
						"output":      ev.ToolOutput,
						"duration_ms": ev.Duration.Milliseconds(),
					}
					if ev.Err != nil {
						data["error"] = ev.Err.Error()
					}
					sseEmit(w, "tool_end", data)

				case agent.EventModelStart:
					sseEmit(w, "model_start", nil)

				case agent.EventModelEnd:
					sseEmit(w, "model_end", map[string]string{"stop_reason": ev.StopReason})

				case agent.EventInvokeEnd:
					if ev.Err != nil {
						sseEmit(w, "error", map[string]string{"error": ev.Err.Error()})
					} else {
						sseEmit(w, "done", map[string]any{
							"input_tokens":  ev.Usage.InputTokens,
							"output_tokens": ev.Usage.OutputTokens,
						})
					}
				}
			}
		})
	})

	log.Fatal(app.Listen(":3000"))
}
