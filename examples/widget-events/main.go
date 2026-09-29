// Example: WidgetBlock — emit structured widget data from a tool handler
// alongside its text response. The agent streams events to the terminal,
// printing text chunks as they arrive and pretty-printing widget payloads.
//
// A second turn verifies that conversation history, including the stored
// WidgetBlock, survives across invocations.
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

	// salesReportTool returns a plain-text summary for the model and emits a
	// chart widget so a UI can render the raw data alongside the answer.
	salesReportTool := tool.New(
		"get_sales_report",
		"Returns a quarterly sales report for a given year. Emits a chart widget with the raw data.",
		func(ctx context.Context, input salesInput) (string, error) {
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

			if err := agent.EmitWidget(ctx, agent.WidgetBlock{
				Type:    "chart",
				Payload: payload,
			}); err != nil {
				return "", fmt.Errorf("emit widget: %w", err)
			}

			return fmt.Sprintf(
				"Sales report for %d: Q1 €142k, Q2 €189k, Q3 €203k, Q4 €251k. "+
					"Total revenue €785k, total costs €463k, net €322k.",
				input.Year,
			), nil
		},
	)

	a, err := agent.New(
		bedrock.Must(bedrock.Cheapest()),
		"You are a sales analyst. When asked about sales data, use the get_sales_report tool.",
		agent.WithTools(salesReportTool),
		agent.WithConversationStore(conversation.NewInMemory()),
	)
	if err != nil {
		log.Fatal(err)
	}

	ask(a, 1, "Can you give me the sales report for 2024?")
	ask(a, 2, "Which quarter had the highest revenue, and by how much did it beat Q1?")
}

// ask sends one message and streams the response to stdout.
func ask(a *agent.Agent, turn int, msg string) {
	fmt.Printf("\nTurn %d › %s\n", turn, msg)
	fmt.Println(strings.Repeat("─", 60))

	ctx := agent.Background().WithConversationID("widget-demo")
	var streamErr error
	for ev, err := range a.Stream(ctx, msg) {
		switch ev.Type {
		case agent.EventText:
			if ev.Text != nil {
				fmt.Print(ev.Text.Content)
			}

		case agent.EventWidget:
			if ev.Widget == nil {
				break
			}
			fmt.Printf("\n\n📊 widget  call=%q type=%q\n", ev.Widget.CallID, ev.Widget.Type)
			var pretty any
			if err := json.Unmarshal(ev.Widget.Payload, &pretty); err == nil {
				out, _ := json.MarshalIndent(pretty, "   ", "  ")
				fmt.Printf("   %s\n", out)
			}

		case agent.EventEnd:
			fmt.Printf("\n\n%s\n", strings.Repeat("─", 60))
			if ev.Result != nil {
				fmt.Printf("tokens — in: %d  out: %d\n",
					ev.Result.Usage.InputTokens, ev.Result.Usage.OutputTokens)
			}
			if ev.Error != nil {
				fmt.Printf("error: %s\n", ev.Error.Message)
			}
		}
		if err != nil {
			streamErr = err
		}
	}
	if streamErr != nil {
		log.Fatal(streamErr)
	}
}
