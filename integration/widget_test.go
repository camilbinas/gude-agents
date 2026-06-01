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
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestIntegration_WidgetBlock exercises the full WidgetBlock pipeline with a
// real LLM. The provider is selected via MODEL_PROVIDER / MODEL_TIER env vars
// (defaults to bedrock/standard).
//
// The test verifies:
//  1. A tool handler emits a WidgetBlock via Context.EmitWidget.
//  2. The EventWidget event appears on InvokeEventStream before EventToolCallEnd.
//  3. The WidgetBlock is stored in conversation history (Conversation.Save).
//  4. A follow-up turn succeeds — confirming that WidgetBlocks in history are
//     stripped before the provider call and don't break subsequent turns.
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
			if c := agent.FromContext(ctx); c != nil {
				if err := c.EmitWidget(block); err != nil {
					return "", err
				}
			}
			return "Q1 €142k, Q2 €189k, Q3 €203k, Q4 €251k. Total €785k.", nil
		},
	)

	store := conversation.NewInMemory()

	a, err := agent.New(p,
		prompt.Text("You are a sales analyst. Use get_sales_report when asked about sales data. Be very brief."),
		[]tool.Tool{reportTool},
		agent.WithConversation(store, "widget-integration"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := agent.NewContext(ctx)

	// ── Turn 1: fetch the report ──────────────────────────────────────────────

	var (
		widgetEvents   []agent.AgentEvent
		toolEndIdx     = -1
		widgetEventIdx = -1
		eventIdx       int
	)

	for ev := range a.InvokeEventStream(c, "Give me the sales report for 2024.") {
		switch ev.Type {
		case agent.EventWidget:
			widgetEventIdx = eventIdx
			widgetEvents = append(widgetEvents, ev)
		case agent.EventToolCallEnd:
			if ev.ToolName == "get_sales_report" {
				toolEndIdx = eventIdx
			}
		case agent.EventInvokeEnd:
			if ev.Err != nil {
				t.Fatalf("turn 1 error: %v", ev.Err)
			}
		}
		eventIdx++
	}

	if len(widgetEvents) == 0 {
		t.Fatal("expected at least one EventWidget event, got none")
	}
	wev := widgetEvents[0]
	if wev.WidgetType != emittedBlock.Type {
		t.Errorf("EventWidget.WidgetType = %q, want %q", wev.WidgetType, emittedBlock.Type)
	}
	if !bytes.Equal(wev.WidgetPayload, emittedBlock.Payload) {
		t.Errorf("EventWidget.WidgetPayload mismatch:\n  got  %s\n  want %s", wev.WidgetPayload, emittedBlock.Payload)
	}
	if toolEndIdx == -1 {
		t.Fatal("EventToolCallEnd for get_sales_report not found")
	}
	if widgetEventIdx >= toolEndIdx {
		t.Errorf("EventWidget (idx %d) must appear before EventToolCallEnd (idx %d)", widgetEventIdx, toolEndIdx)
	}

	history, err := store.Load(ctx, "widget-integration")
	if err != nil {
		t.Fatalf("conversation load: %v", err)
	}
	found := false
	for _, msg := range history {
		for _, cb := range msg.Content {
			if wb, ok := cb.(agent.WidgetBlock); ok && wb.Type == "chart" {
				found = true
			}
		}
	}
	if !found {
		t.Error("WidgetBlock{Type:\"chart\"} not found in conversation history after turn 1")
	}

	// ── Turn 2: follow-up — answers from history, no tool call ───────────────
	// Verifies that WidgetBlocks are stripped before the provider call
	// and don't break subsequent turns.

	var turn2Result strings.Builder
	for ev := range a.InvokeEventStream(c, "Which quarter had the highest revenue?") {
		switch ev.Type {
		case agent.EventTextChunk:
			turn2Result.WriteString(ev.TextChunk)
		case agent.EventInvokeEnd:
			if ev.Err != nil {
				t.Fatalf("turn 2 error: %v", ev.Err)
			}
		}
	}

	if turn2Result.Len() == 0 {
		t.Error("turn 2 returned an empty response")
	}
	if !strings.Contains(strings.ToLower(turn2Result.String()), "q4") {
		t.Logf("turn 2 response did not mention Q4 (may be phrased differently): %s", turn2Result.String())
	}

	t.Logf("turn 1 widget: type=%q payload=%s", wev.WidgetType, wev.WidgetPayload)
	t.Logf("turn 2 response: %s", turn2Result.String())
}
