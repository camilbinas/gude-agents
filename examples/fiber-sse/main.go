// Example: Streaming agent events over SSE with Fiber v3.
//
// Demonstrates how to use EventHook with a Fiber HTTP handler to stream
// tool calls, model lifecycle, and thinking events to the browser in real-time
// alongside the streamed text response.
//
// The agent is created once at startup and shared across requests.
// Each request attaches its own EventHook via context — no concurrency issues.
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

// sseHook streams agent events as Server-Sent Events.
type sseHook struct {
	agent.BaseEventHook
	w *bufio.Writer
}

func (h *sseHook) OnToolCallStart(_ *agent.Context, toolName string, input json.RawMessage) {
	h.send("tool_start", map[string]any{"tool": toolName, "input": json.RawMessage(input)})
}

func (h *sseHook) OnToolCallEnd(_ *agent.Context, toolName string, output string, err error, d time.Duration) {
	data := map[string]any{"tool": toolName, "output": output, "duration_ms": d.Milliseconds()}
	if err != nil {
		data["error"] = err.Error()
	}
	h.send("tool_end", data)
}

func (h *sseHook) OnThinking(_ *agent.Context, chunk string) {
	h.send("thinking", map[string]any{"chunk": chunk})
}

func (h *sseHook) OnModelStart(_ *agent.Context) {
	h.send("model_start", nil)
}

func (h *sseHook) OnModelEnd(_ *agent.Context, stopReason string) {
	h.send("model_end", map[string]any{"stop_reason": stopReason})
}

func (h *sseHook) send(event string, data any) {
	payload, _ := json.Marshal(data)
	fmt.Fprintf(h.w, "event: %s\tdata: %s\n", event, payload)
	h.w.Flush()
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
			hook := &sseHook{w: w}
			ctx := agent.NewContext(c.Context()).WithEventHook(hook)

			err := a.InvokeStream(ctx, q, func(chunk string) {
				payload, _ := json.Marshal(map[string]string{"chunk": chunk})
				fmt.Fprintf(w, "event: text\tdata: %s\n", payload)
				w.Flush()
			})

			if err != nil {
				payload, _ := json.Marshal(map[string]string{"error": err.Error()})
				fmt.Fprintf(w, "event: error\tdata: %s\n\n", payload)
			} else {
				fmt.Fprintf(w, "event: done\tdata: {}\n\n")
			}
			w.Flush()
		})
	})

	log.Fatal(app.Listen(":3000"))
}
