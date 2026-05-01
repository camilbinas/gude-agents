package conversation

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// alwaysSucceedMediaFunc is a MediaSummaryFunc that always returns a text-only message.
func alwaysSucceedMediaFunc(_ context.Context, msg agent.Message) (agent.Message, error) {
	return agent.Message{Role: msg.Role, Content: []agent.ContentBlock{agent.TextBlock{Text: "summarized"}}}, nil
}

// genMessagesWithMedia generates a slice of random messages that may contain media blocks.
func genMessagesWithMedia(t *rapid.T) []agent.Message {
	n := rapid.IntRange(0, 20).Draw(t, "numMessages")
	msgs := make([]agent.Message, n)
	for i := range n {
		msgs[i] = genMessageWithMedia(t)
	}
	return msgs
}

// genContentBlockWithMedia generates a random ContentBlock including ImageBlock and DocumentBlock.
func genContentBlockWithMedia(t *rapid.T) agent.ContentBlock {
	kind := rapid.IntRange(0, 4).Draw(t, "blockKind")
	switch kind {
	case 0:
		return agent.TextBlock{Text: rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "text")}
	case 1:
		return agent.ImageBlock{Source: agent.ImageSource{Base64: "aW1hZ2U=", MIMEType: "image/png"}}
	case 2:
		return agent.DocumentBlock{Source: agent.DocumentSource{Base64: "ZG9j", MIMEType: "application/pdf"}}
	case 3:
		return agent.ToolUseBlock{ToolUseID: "tu_1", Name: "tool", Input: []byte(`{}`)}
	default:
		return agent.ToolResultBlock{ToolUseID: "tu_1", Content: "result"}
	}
}

// genMessageWithMedia generates a random Message that may contain media blocks.
func genMessageWithMedia(t *rapid.T) agent.Message {
	roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
	numBlocks := rapid.IntRange(0, 5).Draw(t, "numBlocks")
	blocks := make([]agent.ContentBlock, numBlocks)
	for i := range numBlocks {
		blocks[i] = genContentBlockWithMedia(t)
	}
	return agent.Message{
		Role:    rapid.SampledFrom(roles).Draw(t, "role"),
		Content: blocks,
	}
}

// TestProperty5_StripNonTextBlocks_GracefulDegradation verifies that for any message,
// stripNonTextBlocks returns the same Role and only TextBlock content.
//
// **Validates: Requirements 1.2**
func TestProperty5_StripNonTextBlocks_GracefulDegradation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		msg := genMessageWithMedia(rt)
		result := stripNonTextBlocks(msg)

		// Property: Role is preserved
		if result.Role != msg.Role {
			rt.Fatalf("role changed: input=%q, output=%q", msg.Role, result.Role)
		}

		// Property: All output blocks are TextBlock
		for i, b := range result.Content {
			if _, ok := b.(agent.TextBlock); !ok {
				rt.Fatalf("block %d is %T, expected TextBlock", i, b)
			}
		}
	})
}

// TestProperty3_PreprocessMediaMessages_LengthPreservation verifies that for any
// message slice, preprocessMediaMessages returns a slice of the same length.
//
// **Validates: Requirements 1.2**
func TestProperty3_PreprocessMediaMessages_LengthPreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		msgs := genMessagesWithMedia(rt)
		s, err := NewSummary(NewInMemory(), 100, dummySummaryFunc, WithMediaSummaryFunc(alwaysSucceedMediaFunc))
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}
		result := s.preprocessMediaMessages(context.Background(), msgs)
		if len(result) != len(msgs) {
			rt.Fatalf("length changed: input=%d, output=%d", len(msgs), len(result))
		}
	})
}

// TestProperty4_PreprocessMediaMessages_TextOnlyUnchanged verifies that text-only
// messages pass through preprocessMediaMessages without modification.
//
// **Validates: Requirements 1.2**
func TestProperty4_PreprocessMediaMessages_TextOnlyUnchanged(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a text-only message
		msg := agent.Message{
			Role:    rapid.SampledFrom([]agent.Role{agent.RoleUser, agent.RoleAssistant}).Draw(rt, "role"),
			Content: []agent.ContentBlock{agent.TextBlock{Text: rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "text")}},
		}
		msgs := []agent.Message{msg}
		s, err := NewSummary(NewInMemory(), 100, dummySummaryFunc, WithMediaSummaryFunc(alwaysSucceedMediaFunc))
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}
		result := s.preprocessMediaMessages(context.Background(), msgs)
		if len(result) != 1 {
			rt.Fatalf("expected 1 message, got %d", len(result))
		}
		// Text-only messages should pass through unchanged
		if result[0].Role != msg.Role {
			rt.Fatalf("role changed: %q -> %q", msg.Role, result[0].Role)
		}
		if len(result[0].Content) != len(msg.Content) {
			rt.Fatalf("content length changed: %d -> %d", len(msg.Content), len(result[0].Content))
		}
		tb, ok := result[0].Content[0].(agent.TextBlock)
		if !ok {
			rt.Fatal("expected TextBlock")
		}
		origTB := msg.Content[0].(agent.TextBlock)
		if tb.Text != origTB.Text {
			rt.Fatalf("text changed: %q -> %q", origTB.Text, tb.Text)
		}
	})
}

// TestProperty6_PreprocessMediaMessages_NoMutationOfInput verifies that
// preprocessMediaMessages does not mutate the input message slice.
//
// **Validates: Requirements 1.2**
func TestProperty6_PreprocessMediaMessages_NoMutationOfInput(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		msgs := genMessagesWithMedia(rt)
		// Deep copy for comparison
		original := make([]agent.Message, len(msgs))
		for i, m := range msgs {
			content := make([]agent.ContentBlock, len(m.Content))
			copy(content, m.Content)
			original[i] = agent.Message{Role: m.Role, Content: content}
		}

		s, err := NewSummary(NewInMemory(), 100, dummySummaryFunc, WithMediaSummaryFunc(alwaysSucceedMediaFunc))
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}
		_ = s.preprocessMediaMessages(context.Background(), msgs)

		// Verify input was not mutated
		if len(msgs) != len(original) {
			rt.Fatalf("input slice length changed: %d -> %d", len(original), len(msgs))
		}
		for i := range msgs {
			if msgs[i].Role != original[i].Role {
				rt.Fatalf("msg[%d].Role mutated: %q -> %q", i, original[i].Role, msgs[i].Role)
			}
			if len(msgs[i].Content) != len(original[i].Content) {
				rt.Fatalf("msg[%d].Content length mutated: %d -> %d", i, len(original[i].Content), len(msgs[i].Content))
			}
		}
	})
}

// TestProperty1_PreprocessMediaMessages_RolePreservation verifies that messages
// with non-text content have the same Role in the result after preprocessing.
//
// **Validates: Requirements 1.2**
func TestProperty1_PreprocessMediaMessages_RolePreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a message with non-text content
		role := rapid.SampledFrom([]agent.Role{agent.RoleUser, agent.RoleAssistant}).Draw(rt, "role")
		msg := agent.Message{
			Role: role,
			Content: []agent.ContentBlock{
				agent.TextBlock{Text: "some text"},
				agent.ImageBlock{Source: agent.ImageSource{Base64: "aW1hZ2U=", MIMEType: "image/png"}},
			},
		}
		msgs := []agent.Message{msg}

		s, err := NewSummary(NewInMemory(), 100, dummySummaryFunc, WithMediaSummaryFunc(alwaysSucceedMediaFunc))
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}
		result := s.preprocessMediaMessages(context.Background(), msgs)
		if result[0].Role != role {
			rt.Fatalf("role not preserved: expected %q, got %q", role, result[0].Role)
		}
	})
}

// TestProperty2_PreprocessMediaMessages_TextOnlyOutput verifies that messages
// with non-text content have only TextBlock in the result after preprocessing.
//
// **Validates: Requirements 1.2**
func TestProperty2_PreprocessMediaMessages_TextOnlyOutput(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate messages that contain non-text content
		numMsgs := rapid.IntRange(1, 10).Draw(rt, "numMsgs")
		msgs := make([]agent.Message, numMsgs)
		for i := range numMsgs {
			role := rapid.SampledFrom([]agent.Role{agent.RoleUser, agent.RoleAssistant}).Draw(rt, fmt.Sprintf("role_%d", i))
			msgs[i] = agent.Message{
				Role: role,
				Content: []agent.ContentBlock{
					agent.TextBlock{Text: "text"},
					agent.ImageBlock{Source: agent.ImageSource{Base64: "aW1hZ2U=", MIMEType: "image/png"}},
				},
			}
		}

		s, err := NewSummary(NewInMemory(), 100, dummySummaryFunc, WithMediaSummaryFunc(alwaysSucceedMediaFunc))
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}
		result := s.preprocessMediaMessages(context.Background(), msgs)

		for i, m := range result {
			for j, b := range m.Content {
				if _, ok := b.(agent.TextBlock); !ok {
					rt.Fatalf("result[%d].Content[%d] is %T, expected TextBlock", i, j, b)
				}
			}
		}
	})
}

// TestProperty7_DisabledByDefault verifies that when mediaSummaryFunc is nil,
// the summarization flow behaves identically to the current implementation.
// Messages are passed to SummaryFunc without any media preprocessing.
//
// **Validates: Requirements 1.2**
func TestProperty7_DisabledByDefault(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a threshold and message count that will trigger summarization
		threshold := rapid.IntRange(3, 10).Draw(rt, "threshold")
		internalThreshold := threshold * 2
		triggerThreshold := (internalThreshold * 80) / 100
		if triggerThreshold < 2 {
			triggerThreshold = 2
		}
		msgCount := rapid.IntRange(triggerThreshold, triggerThreshold+5).Draw(rt, "msgCount")

		store := NewInMemory()
		ctx := context.Background()

		// Track what SummaryFunc receives
		var receivedMsgs []agent.Message
		var mu sync.Mutex
		done := make(chan struct{}, 1)

		summaryFn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
			mu.Lock()
			receivedMsgs = make([]agent.Message, len(msgs))
			copy(receivedMsgs, msgs)
			mu.Unlock()
			select {
			case done <- struct{}{}:
			default:
			}
			return [2]agent.Message{
				{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "summary"}}},
				{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "ok"}}},
			}, nil
		}

		// Create Summary WITHOUT WithMediaSummaryFunc
		s, err := NewSummary(store, threshold, summaryFn)
		if err != nil {
			rt.Fatalf("NewSummary: %v", err)
		}

		// Build messages - some with images
		msgs := make([]agent.Message, msgCount)
		for i := range msgs {
			if i%3 == 0 {
				msgs[i] = agent.Message{
					Role: agent.RoleUser,
					Content: []agent.ContentBlock{
						agent.TextBlock{Text: fmt.Sprintf("msg%d", i)},
						agent.ImageBlock{Source: agent.ImageSource{Base64: "aW1hZ2U=", MIMEType: "image/png"}},
					},
				}
			} else {
				msgs[i] = agent.Message{
					Role:    agent.RoleUser,
					Content: []agent.ContentBlock{agent.TextBlock{Text: fmt.Sprintf("msg%d", i)}},
				}
			}
		}

		if err := s.Save(ctx, "conv", msgs); err != nil {
			rt.Fatalf("Save: %v", err)
		}

		// Wait for summarization
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			rt.Fatal("summarization did not complete")
		}

		s.Wait()

		mu.Lock()
		defer mu.Unlock()

		// Property: SummaryFunc received the messages as-is (no media preprocessing)
		// Messages with images should still have ImageBlock content
		if len(receivedMsgs) != msgCount {
			rt.Fatalf("expected SummaryFunc to receive %d messages, got %d", msgCount, len(receivedMsgs))
		}

		// Verify messages with images still have their ImageBlock
		for i := range receivedMsgs {
			if i%3 == 0 && i < msgCount {
				hasImage := false
				for _, b := range receivedMsgs[i].Content {
					if _, ok := b.(agent.ImageBlock); ok {
						hasImage = true
						break
					}
				}
				if !hasImage {
					rt.Fatalf("expected message %d to still have ImageBlock (no preprocessing), but it was removed", i)
				}
			}
		}
	})
}
