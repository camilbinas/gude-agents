// Example: WidgetBlock — emit structured widget data from a tool handler
// alongside its text response. The agent streams events to the terminal,
// printing text chunks as they arrive and pretty-printing any widget payloads
// when an EventWidget event is received.
//
// A second turn is sent to verify that conversation history (including the
// stored WidgetBlock) survives across invocations.
//
// This pattern lets a UI route text to a chat bubble and the widget payload
// to a chart renderer (or any other component) without extra plumbing.
//
// Run:
//
//	go run ./widget-events
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/joho/godotenv"
)

// SalesData is the payload emitted as a "chart" widget.
type SalesData struct {
	Title  string       `json:"title"`
	Labels []string     `json:"labels"`
	Series []DataSeries `json:"series"`
}

// DataSeries holds one data series for the chart.
type DataSeries struct {
	Name   string    `json:"name"`
	Values []float64 `json:"values"`
}

func main() {
	godotenv.Load() //nolint

	type salesInput struct {
		Year int `json:"year" jsonschema:"description=The year to report on"`
	}

	// salesReportTool returns a plain-text summary for the LLM AND emits a
	// "chart" WidgetBlock so that a UI can render an interactive chart
	// alongside the assistant's answer.
	salesReportTool := tool.New(
		"get_sales_report",
		"Returns a quarterly sales report for a given year. Emits a chart widget with the raw data.",
		func(ctx context.Context, input salesInput) (string, error) {
			// Fake data — in a real tool this would come from a database.
			data := SalesData{
				Title:  fmt.Sprintf("Quarterly Sales %d", input.Year),
				Labels: []string{"Q1", "Q2", "Q3", "Q4"},
				Series: []DataSeries{
					{Name: "Revenue (€k)", Values: []float64{142, 189, 203, 251}},
					{Name: "Costs (€k)", Values: []float64{98, 112, 119, 134}},
				},
			}

			payload, err := json.Marshal(data)
			if err != nil {
				return "", fmt.Errorf("marshal chart data: %w", err)
			}

			// Emit the widget. This triggers an EventWidget event on the
			// InvokeEventStream channel and stores the WidgetBlock inline in
			// the assistant message so it persists in conversation history.
			// agent.FromContext extracts the *agent.Context from the stdlib ctx.
			if c := agent.FromContext(ctx); c != nil {
				if err := c.EmitWidget(agent.WidgetBlock{
					Type:    "chart",
					Payload: payload,
				}); err != nil {
					return "", fmt.Errorf("emit widget: %w", err)
				}
			}

			// Return a plain-text summary for the LLM to use in its answer.
			return fmt.Sprintf(
				"Sales report for %d: Q1 €142k, Q2 €189k, Q3 €203k, Q4 €251k. "+
					"Total revenue €785k, total costs €463k, net €322k.",
				input.Year,
			), nil
		},
	)

	a, err := agent.New(
		bedrock.Must(bedrock.Cheapest()),
		prompt.Text("You are a sales analyst. When asked about sales data, use the get_sales_report tool."),
		[]tool.Tool{salesReportTool},
		agent.WithConversation(conversation.NewInMemory(), "widget-demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.Background()

	// Turn 1 — fetch the report; the tool emits a chart widget.
	ask(a, ctx, 1, "Can you give me the sales report for 2024?")

	// Turn 2 — follow-up that relies on conversation history.
	// No tool call is needed; the agent answers from the stored context,
	// confirming that WidgetBlocks in the history don't break subsequent turns.
	ask(a, ctx, 2, "Which quarter had the highest revenue, and by how much did it beat Q1?")
}

// ask sends one message and streams the response to stdout.
func ask(a *agent.Agent, ctx *agent.Context, turn int, msg string) {
	fmt.Printf("\nTurn %d › %s\n", turn, msg)
	fmt.Println(strings.Repeat("─", 60))

	ch := a.InvokeEventStream(ctx, msg)

	for ev := range ch {
		switch ev.Type {
		case agent.EventTextChunk:
			fmt.Print(ev.TextChunk)

		case agent.EventWidget:
			// A real UI would route this payload to a chart component.
			// Here we just pretty-print it to the terminal.
			fmt.Printf("\n\n📊 widget  type=%q\n", ev.WidgetType)
			var pretty any
			if err := json.Unmarshal(ev.WidgetPayload, &pretty); err == nil {
				out, _ := json.MarshalIndent(pretty, "   ", "  ")
				fmt.Printf("   %s\n", out)
			}

		case agent.EventInvokeEnd:
			fmt.Printf("\n\n%s\n", strings.Repeat("─", 60))
			fmt.Printf("tokens — in: %d  out: %d\n",
				ev.Usage.InputTokens, ev.Usage.OutputTokens)
			if ev.Err != nil {
				log.Fatalf("error: %v", ev.Err)
			}
		}
	}
}
