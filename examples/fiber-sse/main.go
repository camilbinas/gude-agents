// Example: Streaming agent events over SSE with Fiber v3.
//
// Demonstrates how to use Agent.Stream with a Fiber HTTP handler to stream
// tool calls, model lifecycle, thinking, and text chunks to the browser in
// real time. The agent is created once and shared across requests; each
// request gets an independent invocation Context and stream.
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
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/gofiber/fiber/v3"
	"github.com/joho/godotenv"
)

// sseEmit writes a single SSE event with a JSON-encoded payload.
func sseEmit(w *bufio.Writer, event string, data any) {
	payload, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	_ = w.Flush()
}

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.GlobalClaudeSonnet4_6(bedrock.WithThinking(pvdr.ThinkingLow)))

	weather := tool.NewRaw(
		"get_weather",
		"Get current weather for a city",
		func(_ context.Context, input json.RawMessage) (string, error) {
			var params struct {
				City string `json:"city"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return "", fmt.Errorf("decode weather input: %w", err)
			}
			time.Sleep(100 * time.Millisecond) // simulate latency
			return fmt.Sprintf(`{"city":"%s","temp":"22°C","condition":"sunny"}`, params.City), nil
		},
		tool.WithSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "City name"},
			},
			"required": []string{"city"},
		}),
	)

	// Shared agent — created once, used by all requests.
	a, err := agent.New(
		provider,
		"You are a helpful assistant with access to tools.",
		agent.WithTools(weather),
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
			ctx := agent.NewContext(c.Context()).WithDetailedEvents()

			for ev, streamErr := range a.Stream(ctx, q) {
				switch ev.Type {
				case agent.EventText:
					if ev.Text != nil {
						sseEmit(w, "text", map[string]string{"chunk": ev.Text.Content})
					}

				case agent.EventThinking:
					if ev.Thinking != nil {
						sseEmit(w, "thinking", map[string]string{"chunk": ev.Thinking.Content})
					}

				case agent.EventToolStart:
					if ev.Tool != nil {
						sseEmit(w, "tool_start", map[string]any{
							"call_id": ev.Tool.CallID,
							"tool":    ev.Tool.Name,
							"input":   ev.Tool.Input,
						})
					}

				case agent.EventToolEnd:
					if ev.Tool != nil {
						data := map[string]any{
							"call_id":     ev.Tool.CallID,
							"tool":        ev.Tool.Name,
							"output":      ev.Tool.Output,
							"duration_ms": ev.Tool.Duration.Milliseconds(),
						}
						if ev.Tool.Error != nil {
							data["error"] = ev.Tool.Error.Message
						}
						sseEmit(w, "tool_end", data)
					}

				case agent.EventModelStart:
					sseEmit(w, "model_start", nil)

				case agent.EventModelEnd:
					if ev.Lifecycle != nil {
						sseEmit(w, "model_end", map[string]string{"stop_reason": ev.Lifecycle.StopReason})
					}

				case agent.EventEnd:
					if ev.Error != nil {
						sseEmit(w, "error", map[string]string{"error": ev.Error.Message})
					} else if ev.Result != nil {
						sseEmit(w, "done", map[string]any{
							"input_tokens":  ev.Result.Usage.InputTokens,
							"output_tokens": ev.Result.Usage.OutputTokens,
						})
					}
				}

				if streamErr != nil && ev.Error == nil {
					sseEmit(w, "error", map[string]string{"error": streamErr.Error()})
				}
			}
		})
	})

	log.Fatal(app.Listen(":3000"))
}
