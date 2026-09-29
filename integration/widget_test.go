package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestIntegration_WidgetBlock exercises the full WidgetBlock pipeline with a
// real LLM. The provider is selected via MODEL_PROVIDER / MODEL_TIER env vars.
func TestIntegration_WidgetBlock(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)

	type reportInput struct {
		Year int `json:"year" description:"The year to report on" required:"true"`
	}
	type chartPayload struct {
		Title  string    `json:"title"`
		Labels []string  `json:"labels"`
		Values []float64 `json:"values"`
	}

	var emittedBlock agent.WidgetBlock
	reportTool := tool.New(
		"get_sales_report",
		"Returns a quarterly sales report for a given year.",
		func(ctx context.Context, in reportInput) (string, error) {
			payload, _ := json.Marshal(chartPayload{
				Title:  "Quarterly Sales",
				Labels: []string{"Q1", "Q2", "Q3", "Q4"},
				Values: []float64{142, 189, 203, 251},
			})
			block := agent.WidgetBlock{Type: "chart", Payload: payload}
			emittedBlock = block
			if err := agent.EmitWidget(ctx, block); err != nil {
				return "", err
			}
			return "Q1 €142k, Q2 €189k, Q3 €203k, Q4 €251k. Total €785k.", nil
		},
	)

	store := conversation.NewInMemory()
	a, err := agent.New(
		p,
		"You are a sales analyst. Use get_sales_report when asked about sales data. Be very brief.",
		agent.WithTools(reportTool),
		agent.WithConversationStore(store),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := agent.NewContext(ctx).WithConversationID("widget-integration")

	var (
		widgetEvents   []agent.WidgetEvent
		toolEndIdx     = -1
		widgetEventIdx = -1
		eventIdx       int
	)
	for event, streamErr := range a.Stream(c, "Give me the sales report for 2024.") {
		if streamErr != nil {
			t.Fatalf("turn 1 error: %v", streamErr)
		}
		switch event.Type {
		case agent.EventWidget:
			widgetEventIdx = eventIdx
			widgetEvents = append(widgetEvents, *event.Widget)
		case agent.EventToolEnd:
			if event.Tool.Name == "get_sales_report" {
				toolEndIdx = eventIdx
			}
		}
		eventIdx++
	}

	if len(widgetEvents) == 0 {
		t.Fatal("expected at least one EventWidget event, got none")
	}
	wev := widgetEvents[0]
	if wev.Type != emittedBlock.Type {
		t.Errorf("EventWidget.Type = %q, want %q", wev.Type, emittedBlock.Type)
	}
	if !bytes.Equal(wev.Payload, emittedBlock.Payload) {
		t.Errorf("EventWidget.Payload mismatch:\n  got  %s\n  want %s", wev.Payload, emittedBlock.Payload)
	}
	if toolEndIdx == -1 {
		t.Fatal("EventToolEnd for get_sales_report not found")
	}
	if widgetEventIdx >= toolEndIdx {
		t.Errorf("EventWidget (idx %d) must appear before EventToolEnd (idx %d)", widgetEventIdx, toolEndIdx)
	}

	history, err := store.Load(ctx, "widget-integration")
	if err != nil {
		t.Fatalf("conversation load: %v", err)
	}
	found := false
	for _, msg := range history.Messages {
		for _, block := range msg.Content {
			if widget, ok := block.(agent.WidgetBlock); ok && widget.Type == "chart" {
				found = true
			}
		}
	}
	if !found {
		t.Error("WidgetBlock{Type:\"chart\"} not found in conversation history after turn 1")
	}

	var turn2Result strings.Builder
	for event, streamErr := range a.Stream(c, "Which quarter had the highest revenue?") {
		if streamErr != nil {
			t.Fatalf("turn 2 error: %v", streamErr)
		}
		if event.Type == agent.EventText {
			turn2Result.WriteString(event.Text.Content)
		}
	}

	if turn2Result.Len() == 0 {
		t.Error("turn 2 returned an empty response")
	}
	if !strings.Contains(strings.ToLower(turn2Result.String()), "q4") {
		t.Logf("turn 2 response did not mention Q4 (may be phrased differently): %s", turn2Result.String())
	}

	t.Logf("turn 1 widget: type=%q payload=%s", wev.Type, wev.Payload)
	t.Logf("turn 2 response: %s", turn2Result.String())
}
