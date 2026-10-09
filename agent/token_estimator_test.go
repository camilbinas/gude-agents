package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

func estimateChars(t testing.TB, req ModelRequest) int {
	t.Helper()
	got, err := CharEstimator{}.EstimateTokens(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return got
}

func TestCharEstimator_EmptyRequestIsZero(t *testing.T) {
	if got := estimateChars(t, ModelRequest{}); got != 0 {
		t.Fatalf("expected 0 for empty request, got %d", got)
	}
}

func TestCharEstimator_CeilRounding(t *testing.T) {
	tests := []struct {
		name   string
		system string
		want   int
	}{
		{"1 char", "a", 1},
		{"4 chars", "abcd", 1},
		{"5 chars", "abcde", 2},
		{"8 chars", "abcdefgh", 2},
		{"9 chars", "abcdefghi", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := estimateChars(t, ModelRequest{System: tt.system}); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCharEstimator_CountsToolCallAndResultContent(t *testing.T) {
	spec := tool.Spec{Name: "t", Description: "d", InputSchema: map[string]any{"type": "object"}}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}

	tests := []struct {
		name  string
		req   ModelRequest
		chars int
	}{
		{
			name:  "system",
			req:   ModelRequest{System: "sys"},
			chars: 3,
		},
		{
			name: "text blocks across messages",
			req: ModelRequest{Messages: []Message{
				{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "aaa"}}},
				{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "bb"}}},
			}},
			chars: 5,
		},
		{
			name: "tool use counts id, name, and raw input",
			req: ModelRequest{Messages: []Message{
				{Role: RoleAssistant, Content: []ContentBlock{
					ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: []byte(`{"q":"x"}`)},
				}},
			}},
			chars: len("tu-1") + len("search") + len(`{"q":"x"}`),
		},
		{
			name: "tool result counts id and content",
			req: ModelRequest{Messages: []Message{
				{Role: RoleUser, Content: []ContentBlock{
					ToolResultBlock{ToolUseID: "tu-1", Content: "result text"},
				}},
			}},
			chars: len("tu-1") + len("result text"),
		},
		{
			name:  "tool spec counts serialized JSON",
			req:   ModelRequest{Tools: []tool.Spec{spec}},
			chars: len(specJSON),
		},
		{
			name: "all sources combined",
			req: ModelRequest{
				System: "sys",
				Messages: []Message{
					{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "hi"}}},
					{Role: RoleAssistant, Content: []ContentBlock{
						ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: []byte(`{}`)},
					}},
					{Role: RoleUser, Content: []ContentBlock{
						ToolResultBlock{ToolUseID: "tu-1", Content: "done"},
					}},
				},
				Tools: []tool.Spec{spec},
			},
			chars: len("sys") + len("hi") +
				len("tu-1") + len("search") + len(`{}`) +
				len("tu-1") + len("done") +
				len(specJSON),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := 0
			if tt.chars > 0 {
				want = (tt.chars + 3) / 4
			}
			if got := estimateChars(t, tt.req); got != want {
				t.Fatalf("got %d tokens, want %d (chars=%d)", got, want, tt.chars)
			}
		})
	}
}

func TestCharEstimator_MalformedToolInputCountedByByteLength(t *testing.T) {
	malformed := []byte(`{"q": not json`)
	req := ModelRequest{Messages: []Message{
		{Role: RoleAssistant, Content: []ContentBlock{ToolUseBlock{Input: malformed}}},
	}}

	want := (len(malformed) + 3) / 4
	if got := estimateChars(t, req); got != want {
		t.Fatalf("got %d, want %d for %d malformed bytes", got, want, len(malformed))
	}
}

func TestCharEstimator_MonotonicAsPayloadGrows(t *testing.T) {
	req := ModelRequest{}
	prev := estimateChars(t, req)

	steps := []func(*ModelRequest){
		func(r *ModelRequest) { r.System = "system prompt" },
		func(r *ModelRequest) {
			r.Messages = append(r.Messages, Message{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "question"}}})
		},
		func(r *ModelRequest) {
			r.Messages = append(r.Messages, Message{Role: RoleAssistant, Content: []ContentBlock{
				ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: []byte(`{"q":"question"}`)},
			}})
		},
		func(r *ModelRequest) {
			r.Messages = append(r.Messages, Message{Role: RoleUser, Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "tu-1", Content: strings.Repeat("r", 100)},
			}})
		},
		func(r *ModelRequest) { r.Tools = append(r.Tools, tool.Spec{Name: "search"}) },
	}

	for i, step := range steps {
		step(&req)
		got := estimateChars(t, req)
		if got < prev {
			t.Fatalf("step %d decreased estimate from %d to %d", i, prev, got)
		}
		prev = got
	}
}

// TestProperty_NonNegativeEstimation verifies that every valid generated
// request, including tool-use and tool-result blocks, estimates without error
// to a non-negative count.
func TestProperty_NonNegativeEstimation(t *testing.T) {
	estimator := CharEstimator{}

	rapid.Check(t, func(t *rapid.T) {
		params := drawModelRequest(t)

		result, err := estimator.EstimateTokens(context.Background(), params)
		if err != nil {
			t.Fatalf("EstimateTokens returned unexpected error: %v", err)
		}
		if result < 0 {
			t.Fatalf("EstimateTokens returned negative value: %d", result)
		}
	})
}

func BenchmarkCharEstimator_100KChars(b *testing.B) {
	est := CharEstimator{}
	req := ModelRequest{System: strings.Repeat("x", 100_000)}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = est.EstimateTokens(context.Background(), req)
	}
}

// drawModelRequest generates a random ModelRequest with varying message counts,
// text lengths, and tool specs.
func drawModelRequest(t *rapid.T) ModelRequest {
	system := rapid.String().Draw(t, "system")

	numMessages := rapid.IntRange(0, 50).Draw(t, "numMessages")
	messages := make([]Message, numMessages)
	for i := range messages {
		messages[i] = drawMessage(t, i)
	}

	numTools := rapid.IntRange(0, 20).Draw(t, "numTools")
	tools := make([]tool.Spec, numTools)
	for i := range tools {
		tools[i] = drawToolSpec(t, i)
	}

	return ModelRequest{
		Messages: messages,
		System:   system,
		Tools:    tools,
	}
}

// drawMessage generates a random Message with mixed content blocks.
func drawMessage(t *rapid.T, idx int) Message {
	roles := []Role{RoleUser, RoleAssistant}
	role := rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", idx))

	numBlocks := rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("numBlocks_%d", idx))
	content := make([]ContentBlock, numBlocks)
	for i := range content {
		switch rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("blockType_%d_%d", idx, i)) {
		case 0:
			content[i] = TextBlock{Text: rapid.String().Draw(t, fmt.Sprintf("text_%d_%d", idx, i))}
		case 1:
			content[i] = ToolUseBlock{
				ToolUseID: rapid.StringMatching(`tu-[a-z0-9]{4}`).Draw(t, fmt.Sprintf("toolUseID_%d_%d", idx, i)),
				Name:      rapid.StringMatching(`[a-z_]{2,12}`).Draw(t, fmt.Sprintf("toolUseName_%d_%d", idx, i)),
				Input:     []byte(rapid.String().Draw(t, fmt.Sprintf("toolInput_%d_%d", idx, i))),
			}
		default:
			content[i] = ToolResultBlock{
				ToolUseID: rapid.StringMatching(`tu-[a-z0-9]{4}`).Draw(t, fmt.Sprintf("resultID_%d_%d", idx, i)),
				Content:   rapid.String().Draw(t, fmt.Sprintf("resultContent_%d_%d", idx, i)),
			}
		}
	}

	return Message{Role: role, Content: content}
}

// drawToolSpec generates a random tool.Spec.
func drawToolSpec(t *rapid.T, idx int) tool.Spec {
	name := rapid.StringMatching(`[a-z_]{3,20}`).Draw(t, fmt.Sprintf("toolName_%d", idx))
	desc := rapid.StringMatching(`[a-zA-Z0-9 ]{0,100}`).Draw(t, fmt.Sprintf("toolDesc_%d", idx))

	numProps := rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("numProps_%d", idx))
	props := make(map[string]any, numProps)
	for i := 0; i < numProps; i++ {
		propName := rapid.StringMatching(`[a-z]{3,10}`).Draw(t, fmt.Sprintf("propName_%d_%d", idx, i))
		props[propName] = map[string]any{
			"type":        "string",
			"description": rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, fmt.Sprintf("propDesc_%d_%d", idx, i)),
		}
	}

	return tool.Spec{
		Name:        name,
		Description: desc,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": props,
		},
	}
}
