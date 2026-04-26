package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

// TestNormalize_NilInput verifies that nil input returns nil.
func TestNormalize_NilInput(t *testing.T) {
	for _, strategy := range []NormStrategy{NormMerge, NormFill, NormRemove} {
		got := NormalizeMessages(nil, strategy)
		if got != nil {
			t.Errorf("strategy %d: expected nil, got %v", strategy, got)
		}
	}
}

// TestNormalize_EmptyInput verifies that empty input returns an empty slice.
func TestNormalize_EmptyInput(t *testing.T) {
	for _, strategy := range []NormStrategy{NormMerge, NormFill, NormRemove} {
		got := NormalizeMessages([]Message{}, strategy)
		if got == nil {
			t.Errorf("strategy %d: expected non-nil empty slice, got nil", strategy)
		}
		if len(got) != 0 {
			t.Errorf("strategy %d: expected length 0, got %d", strategy, len(got))
		}
	}
}

// TestNormalize_AlreadyAlternating verifies that a valid alternating sequence
// passes through unchanged for each strategy.
func TestNormalize_AlreadyAlternating(t *testing.T) {
	input := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "hello"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "hi"}}},
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "bye"}}},
	}

	strategies := []struct {
		name     string
		strategy NormStrategy
	}{
		{"Merge", NormMerge},
		{"Fill", NormFill},
		{"Remove", NormRemove},
	}

	for _, s := range strategies {
		t.Run(s.name, func(t *testing.T) {
			got := NormalizeMessages(input, s.strategy)
			if len(got) != len(input) {
				t.Fatalf("expected %d messages, got %d", len(input), len(got))
			}
			for i := range input {
				if got[i].Role != input[i].Role {
					t.Errorf("message %d: expected role %q, got %q", i, input[i].Role, got[i].Role)
				}
				if !reflect.DeepEqual(got[i].Content, input[i].Content) {
					t.Errorf("message %d: content mismatch", i)
				}
			}
		})
	}
}

// TestNormalize_SingleAssistantMessage verifies that a single assistant message
// gets a synthetic user message prepended for each strategy.
func TestNormalize_SingleAssistantMessage(t *testing.T) {
	input := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "I'm an assistant"}}},
	}

	syntheticUser := Message{
		Role:    RoleUser,
		Content: []ContentBlock{TextBlock{Text: "Continue."}},
	}

	strategies := []struct {
		name     string
		strategy NormStrategy
	}{
		{"Merge", NormMerge},
		{"Fill", NormFill},
		{"Remove", NormRemove},
	}

	for _, s := range strategies {
		t.Run(s.name, func(t *testing.T) {
			got := NormalizeMessages(input, s.strategy)
			if len(got) != 2 {
				t.Fatalf("expected 2 messages, got %d", len(got))
			}
			if !reflect.DeepEqual(got[0], syntheticUser) {
				t.Errorf("expected synthetic user message %+v, got %+v", syntheticUser, got[0])
			}
			if got[1].Role != RoleAssistant {
				t.Errorf("expected second message role %q, got %q", RoleAssistant, got[1].Role)
			}
			if !reflect.DeepEqual(got[1].Content, input[0].Content) {
				t.Errorf("expected original assistant content preserved")
			}
		})
	}
}

// TestNormalize_MergeTwoConsecutiveUserMessages verifies that two consecutive
// user messages are merged into one with combined content blocks.
func TestNormalize_MergeTwoConsecutiveUserMessages(t *testing.T) {
	input := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "first"}}},
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "second"}}},
	}

	got := NormalizeMessages(input, NormMerge)
	if len(got) != 1 {
		t.Fatalf("expected 1 message after merge, got %d", len(got))
	}
	if got[0].Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, got[0].Role)
	}
	expected := []ContentBlock{TextBlock{Text: "first"}, TextBlock{Text: "second"}}
	if !reflect.DeepEqual(got[0].Content, expected) {
		t.Errorf("expected content %+v, got %+v", expected, got[0].Content)
	}
}

// TestNormalize_MergeThreeConsecutiveAssistantMessages verifies that three
// consecutive assistant messages are merged into one in a single pass.
func TestNormalize_MergeThreeConsecutiveAssistantMessages(t *testing.T) {
	input := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "start"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "a1"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "a2"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "a3"}}},
	}

	got := NormalizeMessages(input, NormMerge)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages after merge, got %d", len(got))
	}
	if got[0].Role != RoleUser {
		t.Errorf("expected first message role %q, got %q", RoleUser, got[0].Role)
	}
	if got[1].Role != RoleAssistant {
		t.Errorf("expected second message role %q, got %q", RoleAssistant, got[1].Role)
	}
	expectedContent := []ContentBlock{
		TextBlock{Text: "a1"},
		TextBlock{Text: "a2"},
		TextBlock{Text: "a3"},
	}
	if !reflect.DeepEqual(got[1].Content, expectedContent) {
		t.Errorf("expected merged content %+v, got %+v", expectedContent, got[1].Content)
	}
}

// TestNormalize_FillInsertsSyntheticMessages verifies that the Fill strategy
// inserts correct synthetic messages between same-role pairs.
func TestNormalize_FillInsertsSyntheticMessages(t *testing.T) {
	input := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "u1"}}},
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "u2"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "a1"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "a2"}}},
	}

	got := NormalizeMessages(input, NormFill)

	// Expected: u1, synthetic-assistant, u2, a1, synthetic-user, a2
	if len(got) != 6 {
		t.Fatalf("expected 6 messages after fill, got %d", len(got))
	}

	// Verify alternation
	expectedRoles := []Role{RoleUser, RoleAssistant, RoleUser, RoleAssistant, RoleUser, RoleAssistant}
	for i, role := range expectedRoles {
		if got[i].Role != role {
			t.Errorf("message %d: expected role %q, got %q", i, role, got[i].Role)
		}
	}

	// Verify synthetic messages
	if !reflect.DeepEqual(got[1].Content, []ContentBlock{TextBlock{Text: "Understood."}}) {
		t.Errorf("expected synthetic assistant message with 'Understood.', got %+v", got[1].Content)
	}
	if !reflect.DeepEqual(got[4].Content, []ContentBlock{TextBlock{Text: "Continue."}}) {
		t.Errorf("expected synthetic user message with 'Continue.', got %+v", got[4].Content)
	}

	// Verify original messages preserved
	if !reflect.DeepEqual(got[0].Content, input[0].Content) {
		t.Errorf("original message 0 not preserved")
	}
	if !reflect.DeepEqual(got[2].Content, input[1].Content) {
		t.Errorf("original message 1 not preserved")
	}
	if !reflect.DeepEqual(got[3].Content, input[2].Content) {
		t.Errorf("original message 2 not preserved")
	}
	if !reflect.DeepEqual(got[5].Content, input[3].Content) {
		t.Errorf("original message 3 not preserved")
	}
}

// TestNormalize_RemoveKeepsLastInRun verifies that the Remove strategy keeps
// only the last message in a same-role run.
func TestNormalize_RemoveKeepsLastInRun(t *testing.T) {
	input := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "u1"}}},
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "u2"}}},
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "u3"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "a1"}}},
	}

	got := NormalizeMessages(input, NormRemove)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages after remove, got %d", len(got))
	}
	if got[0].Role != RoleUser {
		t.Errorf("expected first message role %q, got %q", RoleUser, got[0].Role)
	}
	// Only the last user message (u3) should survive
	if !reflect.DeepEqual(got[0].Content, []ContentBlock{TextBlock{Text: "u3"}}) {
		t.Errorf("expected last user message content, got %+v", got[0].Content)
	}
	if got[1].Role != RoleAssistant {
		t.Errorf("expected second message role %q, got %q", RoleAssistant, got[1].Role)
	}
	if !reflect.DeepEqual(got[1].Content, []ContentBlock{TextBlock{Text: "a1"}}) {
		t.Errorf("expected assistant content preserved, got %+v", got[1].Content)
	}
}

// TestNormalize_MixedContentBlocksPreserved verifies that TextBlock, ToolUseBlock,
// and ToolResultBlock content blocks are preserved during normalization.
func TestNormalize_MixedContentBlocksPreserved(t *testing.T) {
	toolInput := json.RawMessage(`{"key":"value"}`)

	input := []Message{
		{Role: RoleUser, Content: []ContentBlock{
			TextBlock{Text: "please run tool"},
		}},
		{Role: RoleAssistant, Content: []ContentBlock{
			TextBlock{Text: "running tool"},
			ToolUseBlock{ToolUseID: "tu1", Name: "my_tool", Input: toolInput},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock{ToolUseID: "tu1", Content: "tool output", IsError: false},
		}},
	}

	strategies := []struct {
		name     string
		strategy NormStrategy
	}{
		{"Merge", NormMerge},
		{"Fill", NormFill},
		{"Remove", NormRemove},
	}

	for _, s := range strategies {
		t.Run(s.name, func(t *testing.T) {
			got := NormalizeMessages(input, s.strategy)
			// Already alternating, so all strategies should preserve the sequence
			if len(got) != 3 {
				t.Fatalf("expected 3 messages, got %d", len(got))
			}

			// Verify assistant message has both TextBlock and ToolUseBlock
			assistantContent := got[1].Content
			if len(assistantContent) != 2 {
				t.Fatalf("expected 2 content blocks in assistant message, got %d", len(assistantContent))
			}
			tb, ok := assistantContent[0].(TextBlock)
			if !ok || tb.Text != "running tool" {
				t.Errorf("expected TextBlock with 'running tool', got %+v", assistantContent[0])
			}
			tub, ok := assistantContent[1].(ToolUseBlock)
			if !ok || tub.ToolUseID != "tu1" || tub.Name != "my_tool" {
				t.Errorf("expected ToolUseBlock tu1/my_tool, got %+v", assistantContent[1])
			}
			if !reflect.DeepEqual(tub.Input, toolInput) {
				t.Errorf("expected tool input %s, got %s", toolInput, tub.Input)
			}

			// Verify user message with ToolResultBlock
			userContent := got[2].Content
			if len(userContent) != 1 {
				t.Fatalf("expected 1 content block in last user message, got %d", len(userContent))
			}
			trb, ok := userContent[0].(ToolResultBlock)
			if !ok || trb.ToolUseID != "tu1" || trb.Content != "tool output" || trb.IsError {
				t.Errorf("expected ToolResultBlock tu1/tool output/false, got %+v", userContent[0])
			}
		})
	}
}

// TestNormalize_MixedContentBlocksMerged verifies that mixed content blocks
// are correctly combined when merging consecutive same-role messages.
func TestNormalize_MixedContentBlocksMerged(t *testing.T) {
	toolInput := json.RawMessage(`{"q":"test"}`)

	input := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "first"}}},
		{Role: RoleAssistant, Content: []ContentBlock{
			ToolUseBlock{ToolUseID: "tu1", Name: "search", Input: toolInput},
		}},
		{Role: RoleAssistant, Content: []ContentBlock{
			TextBlock{Text: "result"},
		}},
	}

	got := NormalizeMessages(input, NormMerge)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages after merge, got %d", len(got))
	}

	// The merged assistant message should have both blocks
	expectedContent := []ContentBlock{
		ToolUseBlock{ToolUseID: "tu1", Name: "search", Input: toolInput},
		TextBlock{Text: "result"},
	}
	if !reflect.DeepEqual(got[1].Content, expectedContent) {
		t.Errorf("expected merged content %+v, got %+v", expectedContent, got[1].Content)
	}
}

// TestNormalize_OriginalSliceNotMutated verifies that the original input slice
// and its messages' Content slices are not modified by NormalizeMessages.
func TestNormalize_OriginalSliceNotMutated(t *testing.T) {
	input := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "u1"}}},
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "u2"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextBlock{Text: "a1"}}},
	}

	// Deep copy the input for comparison
	original := make([]Message, len(input))
	for i, m := range input {
		original[i] = Message{
			Role:    m.Role,
			Content: make([]ContentBlock, len(m.Content)),
		}
		copy(original[i].Content, m.Content)
	}

	strategies := []NormStrategy{NormMerge, NormFill, NormRemove}
	for _, strategy := range strategies {
		NormalizeMessages(input, strategy)

		// Verify the input slice length hasn't changed
		if len(input) != len(original) {
			t.Errorf("strategy %d: input slice length changed from %d to %d", strategy, len(original), len(input))
		}

		// Verify each message is unchanged
		for i := range original {
			if input[i].Role != original[i].Role {
				t.Errorf("strategy %d: message %d role changed from %q to %q", strategy, i, original[i].Role, input[i].Role)
			}
			if !reflect.DeepEqual(input[i].Content, original[i].Content) {
				t.Errorf("strategy %d: message %d content was mutated", strategy, i)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Agent option integration tests (task 3.3)
// ---------------------------------------------------------------------------

// TestWithMessageNormalizer_ValidStrategies verifies that WithMessageNormalizer
// with each valid strategy sets the normStrategy field correctly on the agent.
func TestWithMessageNormalizer_ValidStrategies(t *testing.T) {
	strategies := []struct {
		name     string
		strategy NormStrategy
	}{
		{"NormMerge", NormMerge},
		{"NormFill", NormFill},
		{"NormRemove", NormRemove},
	}

	for _, s := range strategies {
		t.Run(s.name, func(t *testing.T) {
			a, err := New(mockProvider{}, prompt.Text("sys"), nil, WithMessageNormalizer(s.strategy))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.normStrategy == nil {
				t.Fatal("expected normStrategy to be set, got nil")
			}
			if *a.normStrategy != s.strategy {
				t.Errorf("expected normStrategy=%d, got %d", s.strategy, *a.normStrategy)
			}
			if a.normDisabled {
				t.Error("expected normDisabled to be false")
			}
		})
	}
}

// TestWithMessageNormalizer_InvalidStrategy verifies that WithMessageNormalizer
// with an invalid strategy value returns an error from New.
func TestWithMessageNormalizer_InvalidStrategy(t *testing.T) {
	_, err := New(mockProvider{}, prompt.Text("sys"), nil, WithMessageNormalizer(NormStrategy(99)))
	if err == nil {
		t.Fatal("expected error for invalid strategy, got nil")
	}
}

// TestWithoutMessageNormalizer_SetsDisabled verifies that WithoutMessageNormalizer
// sets normDisabled to true on the agent.
func TestWithoutMessageNormalizer_SetsDisabled(t *testing.T) {
	a, err := New(mockProvider{}, prompt.Text("sys"), nil, WithoutMessageNormalizer())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.normDisabled {
		t.Error("expected normDisabled to be true")
	}
}

// TestNormalize_DefaultBehaviorAppliesMerge verifies that the default behavior
// (no normalizer option) will apply Merge strategy. When no option is set,
// normStrategy is nil and normDisabled is false, so runLoop defaults to NormMerge.
func TestNormalize_DefaultBehaviorAppliesMerge(t *testing.T) {
	a, err := New(mockProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// normStrategy is nil (default) and normDisabled is false.
	// This means NormMerge will be applied by default in runLoop.
	if a.normStrategy != nil {
		t.Error("expected normStrategy to be nil by default")
	}
	if a.normDisabled {
		t.Error("expected normDisabled to be false by default")
	}
}

// TestNormalize_WithoutNormalizerPassesRawMessages verifies that
// WithoutMessageNormalizer causes normDisabled to be true, meaning
// raw messages pass through to the provider without normalization.
func TestNormalize_WithoutNormalizerPassesRawMessages(t *testing.T) {
	a, err := New(mockProvider{}, prompt.Text("sys"), nil, WithoutMessageNormalizer())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.normDisabled {
		t.Error("expected normDisabled to be true with WithoutMessageNormalizer")
	}
}
