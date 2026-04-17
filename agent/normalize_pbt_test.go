package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Generators (Task 5.1)
// ---------------------------------------------------------------------------

// genRole generates a random Role (RoleUser or RoleAssistant).
func genRole(t *rapid.T) Role {
	if rapid.Bool().Draw(t, "isUser") {
		return RoleUser
	}
	return RoleAssistant
}

// genContentBlock generates a random ContentBlock (TextBlock, ToolUseBlock, or ToolResultBlock).
func genContentBlock(t *rapid.T) ContentBlock {
	kind := rapid.IntRange(0, 2).Draw(t, "blockKind")
	switch kind {
	case 0:
		return TextBlock{
			Text: rapid.StringMatching(`[a-zA-Z0-9 ]{1,30}`).Draw(t, "text"),
		}
	case 1:
		input, _ := json.Marshal(map[string]string{
			"key": rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "inputVal"),
		})
		return ToolUseBlock{
			ToolUseID: rapid.StringMatching(`tu-[a-z0-9]{4}`).Draw(t, "toolUseID"),
			Name:      rapid.StringMatching(`[a-z_]{2,12}`).Draw(t, "toolName"),
			Input:     json.RawMessage(input),
		}
	default:
		return ToolResultBlock{
			ToolUseID: rapid.StringMatching(`tu-[a-z0-9]{4}`).Draw(t, "resultToolUseID"),
			Content:   rapid.StringMatching(`[a-zA-Z0-9 ]{1,20}`).Draw(t, "resultContent"),
			IsError:   rapid.Bool().Draw(t, "isError"),
		}
	}
}

// genMessage generates a random Message with 1–5 content blocks.
func genMessage(t *rapid.T) Message {
	role := genRole(t)
	numBlocks := rapid.IntRange(1, 5).Draw(t, "numBlocks")
	content := make([]ContentBlock, numBlocks)
	for i := range numBlocks {
		content[i] = genContentBlock(t)
	}
	return Message{Role: role, Content: content}
}

// genMessages generates a random slice of 0–15 messages.
func genMessages(t *rapid.T) []Message {
	n := rapid.IntRange(0, 15).Draw(t, "numMessages")
	msgs := make([]Message, n)
	for i := range n {
		msgs[i] = genMessage(t)
	}
	return msgs
}

// ---------------------------------------------------------------------------
// Property 1: Output validity — user-first and strict alternation (Task 5.2)
// ---------------------------------------------------------------------------

// Feature: message-normalizer, Property 1: Output validity
//
// TestProperty_OutputValidity verifies that for any non-empty message sequence
// and any strategy, the normalized output starts with a user message and no two
// consecutive messages share the same role.
//
// **Validates: Requirements 4.1, 4.2, 4.3, 4.4, 8.4**
func TestProperty_OutputValidity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		msgs := genMessages(rt)
		strategy := NormStrategy(rapid.IntRange(0, 2).Draw(rt, "strategy"))

		result := NormalizeMessages(msgs, strategy)

		if len(msgs) == 0 {
			if len(result) != 0 {
				rt.Fatalf("expected empty result for empty input, got %d messages", len(result))
			}
			return
		}

		if len(result) == 0 {
			rt.Fatal("expected non-empty result for non-empty input")
		}

		// First message must be user
		if result[0].Role != RoleUser {
			rt.Fatalf("expected first message role %q, got %q", RoleUser, result[0].Role)
		}

		// No two consecutive messages share the same role
		for i := 1; i < len(result); i++ {
			if result[i].Role == result[i-1].Role {
				rt.Fatalf("consecutive messages %d and %d share role %q", i-1, i, result[i].Role)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 2: Idempotence — normalizing twice equals normalizing once (Task 5.3)
// ---------------------------------------------------------------------------

// Feature: message-normalizer, Property 2: Idempotence
//
// TestProperty_Idempotence verifies that for any message sequence and any
// strategy, NormalizeMessages(NormalizeMessages(msgs, s), s) produces the same
// result as NormalizeMessages(msgs, s).
//
// **Validates: Requirements 1.2, 2.3, 3.2, 7.4**
func TestProperty_Idempotence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		msgs := genMessages(rt)
		strategy := NormStrategy(rapid.IntRange(0, 2).Draw(rt, "strategy"))

		once := NormalizeMessages(msgs, strategy)
		twice := NormalizeMessages(once, strategy)

		if !reflect.DeepEqual(once, twice) {
			rt.Fatalf("idempotence violated: normalizing twice differs from normalizing once\nonce:  %+v\ntwice: %+v", once, twice)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: Merge content preservation (Task 5.4)
// ---------------------------------------------------------------------------

// collectContentBlocks flattens all ContentBlock values from a message slice.
func collectContentBlocks(msgs []Message) []ContentBlock {
	var blocks []ContentBlock
	for _, m := range msgs {
		blocks = append(blocks, m.Content...)
	}
	return blocks
}

// Feature: message-normalizer, Property 3: Merge content preservation
//
// TestProperty_MergeContentPreservation verifies that for any message sequence,
// applying Merge produces output where every ContentBlock from the input appears
// exactly once in the output in the same relative order (excluding synthetic
// opening message if one was prepended).
//
// **Validates: Requirements 1.1, 1.3, 1.4, 7.1**
func TestProperty_MergeContentPreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		msgs := genMessages(rt)
		result := NormalizeMessages(msgs, NormMerge)

		if len(msgs) == 0 {
			return
		}

		inputBlocks := collectContentBlocks(msgs)

		// If the first input message was not RoleUser, a synthetic opening
		// message was prepended. Skip it when collecting output blocks.
		outputMsgs := result
		if len(msgs) > 0 && msgs[0].Role != RoleUser && len(result) > 0 {
			outputMsgs = result[1:]
		}
		outputBlocks := collectContentBlocks(outputMsgs)

		if !reflect.DeepEqual(inputBlocks, outputBlocks) {
			rt.Fatalf("merge content preservation violated:\ninput blocks:  %+v\noutput blocks: %+v", inputBlocks, outputBlocks)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: Fill content preservation (Task 5.5)
// ---------------------------------------------------------------------------

// isSyntheticMessage checks if a message is a synthetic filler message
// (single TextBlock with "Understood." or "Continue.").
func isSyntheticMessage(m Message) bool {
	if len(m.Content) != 1 {
		return false
	}
	tb, ok := m.Content[0].(TextBlock)
	if !ok {
		return false
	}
	return tb.Text == "Understood." || tb.Text == "Continue."
}

// Feature: message-normalizer, Property 4: Fill content preservation
//
// TestProperty_FillContentPreservation verifies that for any message sequence,
// applying Fill produces output containing every original message's content
// blocks in order; additional messages are synthetic with a single TextBlock
// acknowledgement.
//
// **Validates: Requirements 2.1, 2.2, 2.4, 7.2**
func TestProperty_FillContentPreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		msgs := genMessages(rt)
		result := NormalizeMessages(msgs, NormFill)

		if len(msgs) == 0 {
			return
		}

		// Walk through the output, matching original messages in order.
		// If the first input message was not RoleUser, the first output
		// message is a synthetic opening — skip it.
		outputStart := 0
		if len(msgs) > 0 && msgs[0].Role != RoleUser && len(result) > 0 {
			if !isSyntheticMessage(result[0]) {
				rt.Fatalf("expected synthetic opening message, got %+v", result[0])
			}
			outputStart = 1
		}

		origIdx := 0
		for i := outputStart; i < len(result); i++ {
			if origIdx < len(msgs) && reflect.DeepEqual(result[i].Content, msgs[origIdx].Content) {
				origIdx++
			} else {
				// Must be a synthetic message
				if !isSyntheticMessage(result[i]) {
					rt.Fatalf("output message %d is neither original message %d nor synthetic: %+v", i, origIdx, result[i])
				}
			}
		}

		if origIdx != len(msgs) {
			rt.Fatalf("not all original messages found in output: matched %d of %d", origIdx, len(msgs))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Remove keeps last (Task 5.6)
// ---------------------------------------------------------------------------

// lastOfEachRun computes the expected messages after Remove: for each
// consecutive run of same-role messages, only the last message survives.
func lastOfEachRun(msgs []Message) []Message {
	if len(msgs) == 0 {
		return nil
	}
	var result []Message
	for i := 0; i < len(msgs); i++ {
		// If this is the last message or the next message has a different role,
		// this is the last in its run.
		if i == len(msgs)-1 || msgs[i+1].Role != msgs[i].Role {
			result = append(result, msgs[i])
		}
	}
	return result
}

// Feature: message-normalizer, Property 5: Remove keeps last
//
// TestProperty_RemoveKeepsLast verifies that for any message sequence, applying
// Remove produces output where for each consecutive run of same-role messages
// in the input, only the content blocks of the last message in that run appear.
//
// **Validates: Requirements 3.1, 3.3, 7.3**
func TestProperty_RemoveKeepsLast(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		msgs := genMessages(rt)
		result := NormalizeMessages(msgs, NormRemove)

		if len(msgs) == 0 {
			return
		}

		// Compute expected: first handle opening violation the same way
		// NormalizeMessages does, then compute last-of-each-run.
		var working []Message
		if msgs[0].Role != RoleUser {
			working = append(working, Message{
				Role:    RoleUser,
				Content: []ContentBlock{TextBlock{Text: "Continue."}},
			})
		}
		for _, m := range msgs {
			working = append(working, Message{
				Role:    m.Role,
				Content: append([]ContentBlock(nil), m.Content...),
			})
		}

		expected := lastOfEachRun(working)

		if len(result) != len(expected) {
			rt.Fatalf("remove result length %d != expected %d\nresult:   %+v\nexpected: %+v", len(result), len(expected), result, expected)
		}

		for i := range expected {
			if result[i].Role != expected[i].Role {
				rt.Fatalf("message %d: role %q != expected %q", i, result[i].Role, expected[i].Role)
			}
			if !reflect.DeepEqual(result[i].Content, expected[i].Content) {
				rt.Fatalf("message %d: content mismatch\ngot:      %+v\nexpected: %+v", i, result[i].Content, expected[i].Content)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 6: No input mutation (Task 5.7)
// ---------------------------------------------------------------------------

// deepCopyMessages creates a deep copy of a message slice.
func deepCopyMessages(msgs []Message) []Message {
	if msgs == nil {
		return nil
	}
	cp := make([]Message, len(msgs))
	for i, m := range msgs {
		cp[i] = Message{
			Role:    m.Role,
			Content: make([]ContentBlock, len(m.Content)),
		}
		copy(cp[i].Content, m.Content)
	}
	return cp
}

// Feature: message-normalizer, Property 6: No input mutation
//
// TestProperty_NoInputMutation verifies that for any message sequence and any
// strategy, calling NormalizeMessages does not modify the input slice or any
// message's Content slice.
//
// **Validates: Requirements 6.2**
func TestProperty_NoInputMutation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		msgs := genMessages(rt)
		strategy := NormStrategy(rapid.IntRange(0, 2).Draw(rt, "strategy"))

		original := deepCopyMessages(msgs)

		NormalizeMessages(msgs, strategy)

		if len(msgs) != len(original) {
			rt.Fatalf("input slice length changed from %d to %d", len(original), len(msgs))
		}

		for i := range original {
			if msgs[i].Role != original[i].Role {
				rt.Fatalf("message %d role changed from %q to %q", i, original[i].Role, msgs[i].Role)
			}
			if len(msgs[i].Content) != len(original[i].Content) {
				rt.Fatalf("message %d content length changed from %d to %d", i, len(original[i].Content), len(msgs[i].Content))
			}
			if !reflect.DeepEqual(msgs[i].Content, original[i].Content) {
				rt.Fatalf("message %d content was mutated", i)
			}
		}
	})
}
