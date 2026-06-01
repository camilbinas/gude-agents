package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// **Validates: Requirements 2.2, 2.6, 3.1, 3.2, 4.2, 4.4, 4.5**

// ---------------------------------------------------------------------------
// Shared generators (reuse widgetTypeGen from event_stream_pbt_test.go is not
// possible across files, so we define local helpers here).
// ---------------------------------------------------------------------------

// loopWidgetTypeGen generates a non-empty widget type string for loop PBT tests.
var loopWidgetTypeGen = rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,30}`)

// loopValidWidgetBlockGen generates a WidgetBlock with a non-empty Type.
func loopValidWidgetBlockGen(t *rapid.T) WidgetBlock {
	useNilPayload := rapid.Bool().Draw(t, "useNilPayload")
	var payload json.RawMessage
	if !useNilPayload {
		raw := rapid.SliceOfN(rapid.Byte(), 1, 32).Draw(t, "payloadBytes")
		payload = json.RawMessage(raw)
	}
	return WidgetBlock{
		Type:    loopWidgetTypeGen.Draw(t, "widgetType"),
		Payload: payload,
	}
}

// ---------------------------------------------------------------------------
// Mock conversation that records Save calls.
// ---------------------------------------------------------------------------

// recordingConversation records every Save call so tests can inspect what was
// persisted. It satisfies the Conversation interface.
type recordingConversation struct {
	mu      sync.Mutex
	saved   [][]Message // one entry per Save call
	history map[string][]Message
}

func newRecordingConversation() *recordingConversation {
	return &recordingConversation{history: make(map[string][]Message)}
}

func (r *recordingConversation) Load(_ context.Context, id string) ([]Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	msgs := r.history[id]
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	return cp, nil
}

func (r *recordingConversation) Save(_ context.Context, id string, msgs []Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	r.saved = append(r.saved, cp)
	r.history[id] = cp
	return nil
}

func (r *recordingConversation) List(_ context.Context) ([]string, error) { return nil, nil }
func (r *recordingConversation) Delete(_ context.Context, _ string) error { return nil }

// lastSaved returns the most recently saved message slice, or nil.
func (r *recordingConversation) lastSaved() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.saved) == 0 {
		return nil
	}
	return r.saved[len(r.saved)-1]
}

// ---------------------------------------------------------------------------
// Property 2: EventWidget ordering
//
// For any tool handler that calls EmitWidget, the EventWidget event must
// appear before EventToolCallEnd for the same tool call in the
// InvokeEventStream channel.
// ---------------------------------------------------------------------------

// TestProperty_Loop_EventWidgetOrdering verifies Property 2.
func TestProperty_Loop_EventWidgetOrdering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		block := loopValidWidgetBlockGen(t)

		// Provider: first call returns a single tool call; second call returns
		// a final text answer so the loop terminates.
		sp := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{
				{ToolUseID: "tc-order-1", Name: "widget_tool", Input: json.RawMessage(`{}`)},
			}},
			&ProviderResponse{Text: "done"},
		)

		// Tool handler emits the widget then returns.
		widgetTool := tool.NewRaw(
			"widget_tool",
			"emits a widget",
			map[string]any{"type": "object"},
			func(ctx context.Context, _ json.RawMessage) (string, error) {
				c := FromContext(ctx)
				if c != nil {
					_ = c.EmitWidget(block)
				}
				return "result", nil
			},
		)

		a, err := New(sp, prompt.Text("sys"), []tool.Tool{widgetTool})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ch := a.InvokeEventStream(Background(), "go")
		events := drainEvents(ch)

		// Find the index of EventWidget and EventToolCallEnd for our tool call.
		widgetIdx := -1
		toolEndIdx := -1
		for i, e := range events {
			if e.Type == EventWidget && widgetIdx == -1 {
				widgetIdx = i
			}
			if e.Type == EventToolCallEnd && e.ToolName == "widget_tool" {
				toolEndIdx = i
			}
		}

		if widgetIdx == -1 {
			t.Fatalf("no EventWidget event found in stream; events: %v", eventTypes(events))
		}
		if toolEndIdx == -1 {
			t.Fatalf("no EventToolCallEnd event found for widget_tool; events: %v", eventTypes(events))
		}
		if widgetIdx >= toolEndIdx {
			t.Fatalf("EventWidget (index %d) must appear before EventToolCallEnd (index %d); events: %v",
				widgetIdx, toolEndIdx, eventTypes(events))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: No spurious EventWidget events
//
// For any invocation where no handler calls EmitWidget, the channel must
// contain zero events with Type == EventWidget.
// ---------------------------------------------------------------------------

// TestProperty_Loop_NoSpuriousWidgetEvents verifies Property 4.
func TestProperty_Loop_NoSpuriousWidgetEvents(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Provider: first call returns a tool call; second returns final text.
		sp := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{
				{ToolUseID: "tc-nospurious-1", Name: "plain_tool", Input: json.RawMessage(`{}`)},
			}},
			&ProviderResponse{Text: "done"},
		)

		// Tool handler does NOT call EmitWidget.
		plainTool := tool.NewRaw(
			"plain_tool",
			"does not emit widgets",
			map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				return "plain result", nil
			},
		)

		a, err := New(sp, prompt.Text("sys"), []tool.Tool{plainTool})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ch := a.InvokeEventStream(Background(), "go")
		events := drainEvents(ch)

		for _, e := range events {
			if e.Type == EventWidget {
				t.Fatalf("unexpected EventWidget event found in stream; events: %v", eventTypes(events))
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Widget persistence in conversation
//
// For any valid WidgetBlock emitted during an invocation with a mock
// Conversation, the saved Message.Content must contain that WidgetBlock.
// ---------------------------------------------------------------------------

// TestProperty_Loop_WidgetPersistenceInConversation verifies Property 5.
func TestProperty_Loop_WidgetPersistenceInConversation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		block := loopValidWidgetBlockGen(t)

		// Provider: first call returns a tool call; second returns final text.
		sp := newScriptedProvider(
			&ProviderResponse{ToolCalls: []tool.Call{
				{ToolUseID: "tc-persist-1", Name: "persist_tool", Input: json.RawMessage(`{}`)},
			}},
			&ProviderResponse{Text: "done"},
		)

		// Tool handler emits the widget.
		persistTool := tool.NewRaw(
			"persist_tool",
			"emits a widget for persistence test",
			map[string]any{"type": "object"},
			func(ctx context.Context, _ json.RawMessage) (string, error) {
				c := FromContext(ctx)
				if c != nil {
					_ = c.EmitWidget(block)
				}
				return "persisted", nil
			},
		)

		conv := newRecordingConversation()
		a, err := New(sp, prompt.Text("sys"), []tool.Tool{persistTool},
			WithConversation(conv, "conv-persist-1"),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, invokeErr := a.Invoke(Background(), "go")
		if invokeErr != nil {
			t.Fatalf("Invoke: %v", invokeErr)
		}

		saved := conv.lastSaved()
		if saved == nil {
			t.Fatalf("conversation.Save was never called")
		}

		// Search all saved messages for the emitted WidgetBlock.
		found := false
		for _, msg := range saved {
			for _, cb := range msg.Content {
				if wb, ok := cb.(WidgetBlock); ok {
					if wb.Type == block.Type {
						found = true
						break
					}
				}
			}
			if found {
				break
			}
		}

		if !found {
			t.Fatalf("WidgetBlock{Type:%q} not found in saved messages; saved: %v",
				block.Type, savedSummary(saved))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 10: Concurrent EmitWidget is race-free
//
// Concurrent goroutines calling EmitWidget simultaneously must produce no
// data races. Run with -race to detect races.
// ---------------------------------------------------------------------------

// TestProperty_Loop_ConcurrentEmitWidgetIsRaceFree verifies Property 10.
func TestProperty_Loop_ConcurrentEmitWidgetIsRaceFree(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate N goroutines (2–8) each emitting a widget.
		n := rapid.IntRange(2, 8).Draw(t, "goroutineCount")

		// Build N distinct widget blocks.
		blocks := make([]WidgetBlock, n)
		for i := range n {
			blocks[i] = loopValidWidgetBlockGen(t)
		}

		// Create a fresh accumulator and inject it into a Context.
		acc := &widgetAccumulator{}
		c := Background()
		c.Set(widgetAccumulatorKey{}, acc)

		// Spawn N goroutines all calling EmitWidget concurrently.
		var wg sync.WaitGroup
		wg.Add(n)
		for i := range n {
			go func(idx int) {
				defer wg.Done()
				_ = c.EmitWidget(blocks[idx])
			}(i)
		}
		wg.Wait()

		// Drain the accumulator and verify all N blocks were stored.
		drained := acc.drain()
		if len(drained) != n {
			t.Fatalf("expected %d blocks in accumulator after concurrent EmitWidget, got %d", n, len(drained))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 11: Provider Message Slice contains no WidgetBlocks
//
// For any []Message slice with arbitrary WidgetBlock distributions, stripWidgets
// must return a slice where no Message.Content contains a WidgetBlock and no
// widget-only messages are included.
// ---------------------------------------------------------------------------

// loopMixedContentGen generates a single ContentBlock of a random type
// (TextBlock, WidgetBlock, ToolUseBlock, or ToolResultBlock).
func loopMixedContentGen(t *rapid.T, label string) ContentBlock {
	blockType := rapid.IntRange(0, 3).Draw(t, label+"_blockType")
	switch blockType {
	case 0:
		text := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(t, label+"_text")
		return TextBlock{Text: text}
	case 1:
		widgetType := rapid.StringMatching(`[a-z]{1,20}`).Draw(t, label+"_widgetType")
		var payload json.RawMessage
		if rapid.Bool().Draw(t, label+"_hasPayload") {
			payload = json.RawMessage(`{"key":"value"}`)
		}
		return WidgetBlock{Type: widgetType, Payload: payload}
	case 2:
		return ToolUseBlock{
			ToolUseID: rapid.StringMatching(`[a-z0-9]{4,12}`).Draw(t, label+"_toolUseID"),
			Name:      rapid.StringMatching(`[a-z_]{1,20}`).Draw(t, label+"_toolName"),
			Input:     json.RawMessage(`{}`),
		}
	default:
		return ToolResultBlock{
			ToolUseID: rapid.StringMatching(`[a-z0-9]{4,12}`).Draw(t, label+"_toolResultID"),
			Content:   rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(t, label+"_toolResult"),
		}
	}
}

// loopGenerateMixedMessages generates a slice of Messages with arbitrary mixes
// of content block types, including WidgetBlocks at random positions.
func loopGenerateMixedMessages(t *rapid.T) []Message {
	numMessages := rapid.IntRange(0, 10).Draw(t, "numMessages")
	msgs := make([]Message, numMessages)

	for i := range numMessages {
		role := RoleUser
		if rapid.Bool().Draw(t, "isAssistant") {
			role = RoleAssistant
		}

		numBlocks := rapid.IntRange(1, 5).Draw(t, "numBlocks")
		content := make([]ContentBlock, numBlocks)
		for j := range numBlocks {
			label := "m" + string(rune('0'+i%10)) + "b" + string(rune('0'+j%10))
			content[j] = loopMixedContentGen(t, label)
		}

		msgs[i] = Message{Role: role, Content: content}
	}

	return msgs
}

// TestProperty_StripWidgetsNoWidgetBlocks verifies Property 11:
// For any []Message slice with arbitrary WidgetBlock distributions, stripWidgets
// must return a slice where no Message.Content contains a WidgetBlock and no
// widget-only messages are included.
//
// **Validates: Requirements 6.1, 6.2, 6.3, 6.4**
func TestProperty_StripWidgetsNoWidgetBlocks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := loopGenerateMixedMessages(t)

		// Snapshot original widget counts per message to verify no mutation.
		originalWidgetCounts := make([]int, len(msgs))
		for i, m := range msgs {
			for _, b := range m.Content {
				if _, isWidget := b.(WidgetBlock); isWidget {
					originalWidgetCounts[i]++
				}
			}
		}

		result := stripWidgets(msgs)

		// Assert 1 (Req 6.1, 6.2): no WidgetBlock in any result message.
		for i, m := range result {
			for j, b := range m.Content {
				if _, isWidget := b.(WidgetBlock); isWidget {
					t.Fatalf("stripWidgets returned a WidgetBlock at result[%d].Content[%d]", i, j)
				}
			}
		}

		// Assert 2 (Req 6.3): no empty-content messages in result.
		for i, m := range result {
			if len(m.Content) == 0 {
				t.Fatalf("stripWidgets returned a message with empty Content at result[%d]", i)
			}
		}

		// Assert 3 (Req 6.4): input not mutated — original messages still have their WidgetBlocks.
		for i, m := range msgs {
			actualWidgets := 0
			for _, b := range m.Content {
				if _, isWidget := b.(WidgetBlock); isWidget {
					actualWidgets++
				}
			}
			if actualWidgets != originalWidgetCounts[i] {
				t.Fatalf("stripWidgets mutated input: msgs[%d] had %d widgets before, has %d after",
					i, originalWidgetCounts[i], actualWidgets)
			}
		}

		// Assert 4: result length <= input length (we only remove, never add).
		if len(result) > len(msgs) {
			t.Fatalf("stripWidgets returned more messages (%d) than input (%d)", len(result), len(msgs))
		}

		// Assert 5: all widget-free messages (those with at least one non-widget block
		// and no widget blocks) must appear in the result.
		noWidgetMsgCount := 0
		for _, m := range msgs {
			hasWidget := false
			hasNonWidget := false
			for _, b := range m.Content {
				if _, isWidget := b.(WidgetBlock); isWidget {
					hasWidget = true
				} else {
					hasNonWidget = true
				}
			}
			if !hasWidget && hasNonWidget {
				noWidgetMsgCount++
			}
		}
		if len(result) < noWidgetMsgCount {
			t.Fatalf("stripWidgets dropped widget-free messages: expected at least %d in result, got %d",
				noWidgetMsgCount, len(result))
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// savedSummary returns a compact string representation of saved messages for
// diagnostic output.
func savedSummary(msgs []Message) string {
	var parts []string
	for _, m := range msgs {
		for _, cb := range m.Content {
			switch b := cb.(type) {
			case WidgetBlock:
				parts = append(parts, "WidgetBlock{Type:"+b.Type+"}")
			case TextBlock:
				parts = append(parts, "TextBlock{Text:"+b.Text+"}")
			case ToolUseBlock:
				parts = append(parts, "ToolUseBlock{Name:"+b.Name+"}")
			case ToolResultBlock:
				parts = append(parts, "ToolResultBlock{ID:"+b.ToolUseID+"}")
			default:
				parts = append(parts, "UnknownBlock")
			}
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
