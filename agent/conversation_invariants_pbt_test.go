package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// adversarialProvider returns randomized ProviderResponses designed to stress
// conversation persistence invariants. It can return:
// - Empty text with no tool calls
// - Text-only responses (normal)
// - Tool calls with empty text (normal)
// - Tool calls with non-empty text (normal)
// - Whitespace-only text
type adversarialProvider struct {
	responses []*ProviderResponse
	idx       int
}

func (p *adversarialProvider) Name() string { return "adversarial" }

func (p *adversarialProvider) next() *ProviderResponse {
	if p.idx >= len(p.responses) {
		// Fallback: return text to terminate the loop.
		return &ProviderResponse{Text: "fallback"}
	}
	r := p.responses[p.idx]
	p.idx++
	return r
}

func (p *adversarialProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return p.next(), nil
}

func (p *adversarialProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	r := p.next()
	if cb != nil && r.Text != "" && len(r.ToolCalls) == 0 {
		cb(r.Text)
	}
	return r, nil
}

// trackingConversation records all messages passed to Save.
type trackingConversation struct {
	saved [][]Message
}

func (t *trackingConversation) Load(_ context.Context, _ string) ([]Message, error) {
	return nil, nil
}

func (t *trackingConversation) Save(_ context.Context, _ string, msgs []Message) error {
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	t.saved = append(t.saved, cp)
	return nil
}

func (t *trackingConversation) List(_ context.Context) ([]string, error) { return nil, nil }
func (t *trackingConversation) Delete(_ context.Context, _ string) error { return nil }

func (t *trackingConversation) lastSaved() []Message {
	if len(t.saved) == 0 {
		return nil
	}
	return t.saved[len(t.saved)-1]
}

// genAdversarialResponses generates a sequence of ProviderResponses that
// exercises edge cases: empty text, whitespace text, tool calls with/without
// text, and eventually a terminating response.
func genAdversarialResponses(rt *rapid.T, toolNames []string) []*ProviderResponse {
	// Generate 1-4 tool-call iterations followed by a final response.
	numToolIters := rapid.IntRange(0, 4).Draw(rt, "numToolIters")
	responses := make([]*ProviderResponse, 0, numToolIters+1)

	for i := range numToolIters {
		numCalls := rapid.IntRange(1, 3).Draw(rt, fmt.Sprintf("numCalls_%d", i))
		calls := make([]tool.Call, numCalls)
		for j := range numCalls {
			calls[j] = tool.Call{
				ToolUseID: fmt.Sprintf("tc-%d-%d", i, j),
				Name:      toolNames[j%len(toolNames)],
				Input:     json.RawMessage(`{}`),
			}
		}
		// Randomly include text alongside tool calls.
		text := ""
		if rapid.Bool().Draw(rt, fmt.Sprintf("hasText_%d", i)) {
			text = rapid.OneOf(
				rapid.Just(""),
				rapid.Just("   "),
				rapid.Just("\n"),
				rapid.StringMatching(`[a-zA-Z ]{1,30}`),
			).Draw(rt, fmt.Sprintf("toolText_%d", i))
		}
		responses = append(responses, &ProviderResponse{
			Text:      text,
			ToolCalls: calls,
			Usage:     TokenUsage{InputTokens: 10, OutputTokens: 5},
		})
	}

	// Final response — may be empty, whitespace, or normal text.
	finalText := rapid.OneOf(
		rapid.Just(""),
		rapid.Just("   "),
		rapid.Just("\t\n"),
		rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,100}`),
	).Draw(rt, "finalText")
	responses = append(responses, &ProviderResponse{
		Text:  finalText,
		Usage: TokenUsage{InputTokens: 10, OutputTokens: 5},
	})

	return responses
}

// TestProperty_ConversationNoEmptyTextBlocks verifies that for any sequence of
// adversarial provider responses, the saved conversation never contains a
// TextBlock with empty text.
func TestProperty_ConversationNoEmptyTextBlocks(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolNames := []string{"tool_a", "tool_b", "tool_c"}
		tools := make([]tool.Tool, len(toolNames))
		for i, name := range toolNames {
			tools[i] = tool.NewRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		responses := genAdversarialResponses(rt, toolNames)
		provider := &adversarialProvider{responses: responses}
		store := &trackingConversation{}

		a, err := New(provider, prompt.Text("test"), tools,
			WithConversation(store, "test-conv"),
			WithMaxIterations(10),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		// Run the agent — we don't care about the result, only the saved conversation.
		a.Invoke(Background(), "hello")

		msgs := store.lastSaved()
		for i, msg := range msgs {
			for j, block := range msg.Content {
				if tb, ok := block.(TextBlock); ok && tb.Text == "" {
					rt.Fatalf("empty TextBlock at message[%d].Content[%d] (role=%s)", i, j, msg.Role)
				}
			}
		}
	})
}

// TestProperty_ConversationNoEmptyContentMessages verifies that no saved message
// has an empty Content slice.
func TestProperty_ConversationNoEmptyContentMessages(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolNames := []string{"tool_a", "tool_b"}
		tools := make([]tool.Tool, len(toolNames))
		for i, name := range toolNames {
			tools[i] = tool.NewRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		responses := genAdversarialResponses(rt, toolNames)
		provider := &adversarialProvider{responses: responses}
		store := &trackingConversation{}

		a, err := New(provider, prompt.Text("test"), tools,
			WithConversation(store, "test-conv"),
			WithMaxIterations(10),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		a.Invoke(Background(), "hello")

		msgs := store.lastSaved()
		for i, msg := range msgs {
			if len(msg.Content) == 0 {
				rt.Fatalf("empty Content at message[%d] (role=%s)", i, msg.Role)
			}
		}
	})
}

// TestProperty_ConversationToolResultsHaveMatchingToolUse verifies that every
// ToolResultBlock in the saved conversation has a corresponding ToolUseBlock
// earlier in the conversation.
func TestProperty_ConversationToolResultsHaveMatchingToolUse(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolNames := []string{"tool_a", "tool_b", "tool_c"}
		tools := make([]tool.Tool, len(toolNames))
		for i, name := range toolNames {
			tools[i] = tool.NewRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		responses := genAdversarialResponses(rt, toolNames)
		provider := &adversarialProvider{responses: responses}
		store := &trackingConversation{}

		a, err := New(provider, prompt.Text("test"), tools,
			WithConversation(store, "test-conv"),
			WithMaxIterations(10),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		a.Invoke(Background(), "hello")

		msgs := store.lastSaved()

		// Collect all ToolUseBlock IDs.
		toolUseIDs := make(map[string]bool)
		for _, msg := range msgs {
			for _, block := range msg.Content {
				if tu, ok := block.(ToolUseBlock); ok {
					toolUseIDs[tu.ToolUseID] = true
				}
			}
		}

		// Verify every ToolResultBlock references a known ToolUseBlock.
		for i, msg := range msgs {
			for j, block := range msg.Content {
				if tr, ok := block.(ToolResultBlock); ok {
					if !toolUseIDs[tr.ToolUseID] {
						rt.Fatalf("orphaned ToolResultBlock at message[%d].Content[%d]: ToolUseID=%q not found", i, j, tr.ToolUseID)
					}
				}
			}
		}
	})
}

// TestProperty_ConversationRolesAlternate verifies that saved messages alternate
// between user and assistant roles (with the exception that the first message
// may be either role depending on RAG injection).
func TestProperty_ConversationRolesAlternate(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		toolNames := []string{"tool_a"}
		tools := make([]tool.Tool, len(toolNames))
		for i, name := range toolNames {
			tools[i] = tool.NewRaw(name, name+" desc", map[string]any{"type": "object"},
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return "result", nil
				})
		}

		responses := genAdversarialResponses(rt, toolNames)
		provider := &adversarialProvider{responses: responses}
		store := &trackingConversation{}

		a, err := New(provider, prompt.Text("test"), tools,
			WithConversation(store, "test-conv"),
			WithMaxIterations(10),
		)
		if err != nil {
			rt.Fatalf("failed to create agent: %v", err)
		}

		a.Invoke(Background(), "hello")

		msgs := store.lastSaved()
		if len(msgs) < 2 {
			return // too few messages to check alternation
		}

		for i := 1; i < len(msgs); i++ {
			if msgs[i].Role == msgs[i-1].Role {
				rt.Fatalf("consecutive same-role messages at [%d] and [%d]: both %s", i-1, i, msgs[i].Role)
			}
		}
	})
}
