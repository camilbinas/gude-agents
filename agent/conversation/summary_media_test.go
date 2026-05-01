package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
)

func dummySummaryFunc(_ context.Context, _ []agent.Message) ([2]agent.Message, error) {
	return [2]agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "summary"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "ok"}}},
	}, nil
}

func dummyMediaSummaryFunc(_ context.Context, msg agent.Message) (agent.Message, error) {
	return agent.Message{Role: msg.Role, Content: []agent.ContentBlock{agent.TextBlock{Text: "summarized"}}}, nil
}

func TestMediaSummaryFunc_NilReturnsError(t *testing.T) {
	_, err := NewSummary(NewInMemory(), 5, dummySummaryFunc, WithMediaSummaryFunc(nil))
	if err == nil {
		t.Fatal("expected error when passing nil MediaSummaryFunc, got nil")
	}
}

func TestMediaSummaryFunc_ValidFuncSucceeds(t *testing.T) {
	s, err := NewSummary(NewInMemory(), 5, dummySummaryFunc, WithMediaSummaryFunc(dummyMediaSummaryFunc))
	if err != nil {
		t.Fatalf("expected no error with valid MediaSummaryFunc, got: %v", err)
	}
	if s.mediaSummaryFunc == nil {
		t.Fatal("expected mediaSummaryFunc to be set, got nil")
	}
}

func TestMediaSummaryConcurrency_InvalidValues(t *testing.T) {
	tests := []struct {
		name string
		val  int
	}{
		{"zero", 0},
		{"negative", -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSummary(NewInMemory(), 5, dummySummaryFunc, WithMediaSummaryConcurrency(tc.val))
			if err == nil {
				t.Fatalf("expected error for concurrency=%d, got nil", tc.val)
			}
		})
	}
}

func TestMediaSummaryConcurrency_ValidValues(t *testing.T) {
	tests := []struct {
		name string
		val  int
	}{
		{"one", 1},
		{"five", 5},
		{"ten", 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewSummary(NewInMemory(), 5, dummySummaryFunc, WithMediaSummaryConcurrency(tc.val))
			if err != nil {
				t.Fatalf("expected no error for concurrency=%d, got: %v", tc.val, err)
			}
			if s.mediaSummaryConcurrency != tc.val {
				t.Fatalf("expected mediaSummaryConcurrency=%d, got %d", tc.val, s.mediaSummaryConcurrency)
			}
		})
	}
}

func TestMediaSummaryConcurrency_DefaultIsThree(t *testing.T) {
	s, err := NewSummary(NewInMemory(), 5, dummySummaryFunc)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// When WithMediaSummaryConcurrency is not used, the field stays at zero.
	// The default of 3 is applied at runtime in preprocessMediaMessages.
	if s.mediaSummaryConcurrency != 0 {
		t.Fatalf("expected mediaSummaryConcurrency=0 (default applied at runtime), got %d", s.mediaSummaryConcurrency)
	}
}

func TestHasNonTextContent(t *testing.T) {
	tests := []struct {
		name string
		msg  agent.Message
		want bool
	}{
		{"text only", agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}}, false},
		{"image", agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}}}}, true},
		{"document", agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.DocumentBlock{Source: agent.DocumentSource{Base64: "abc", MIMEType: "application/pdf"}}}}, true},
		{"mixed text and image", agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "look"}, agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}}}}, true},
		{"empty content", agent.Message{Role: agent.RoleUser, Content: nil}, false},
		{"tool use only", agent.Message{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.ToolUseBlock{ToolUseID: "tu_1", Name: "tool", Input: json.RawMessage(`{}`)}}}, false},
		{"tool result only", agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.ToolResultBlock{ToolUseID: "tu_1", Content: "result"}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasNonTextContent(tc.msg)
			if got != tc.want {
				t.Fatalf("hasNonTextContent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStripNonTextBlocks(t *testing.T) {
	t.Run("preserves role", func(t *testing.T) {
		msg := agent.Message{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
			agent.TextBlock{Text: "hello"},
			agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}},
		}}
		got := stripNonTextBlocks(msg)
		if got.Role != agent.RoleAssistant {
			t.Fatalf("expected role %q, got %q", agent.RoleAssistant, got.Role)
		}
	})

	t.Run("keeps text blocks in order", func(t *testing.T) {
		msg := agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{
			agent.TextBlock{Text: "first"},
			agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}},
			agent.TextBlock{Text: "second"},
			agent.DocumentBlock{Source: agent.DocumentSource{Base64: "abc", MIMEType: "application/pdf"}},
			agent.TextBlock{Text: "third"},
		}}
		got := stripNonTextBlocks(msg)
		if len(got.Content) != 3 {
			t.Fatalf("expected 3 text blocks, got %d", len(got.Content))
		}
		texts := []string{"first", "second", "third"}
		for i, b := range got.Content {
			tb, ok := b.(agent.TextBlock)
			if !ok {
				t.Fatalf("block %d is not TextBlock", i)
			}
			if tb.Text != texts[i] {
				t.Fatalf("block %d text = %q, want %q", i, tb.Text, texts[i])
			}
		}
	})

	t.Run("empty content returns nil content", func(t *testing.T) {
		msg := agent.Message{Role: agent.RoleUser, Content: nil}
		got := stripNonTextBlocks(msg)
		if got.Content != nil {
			t.Fatalf("expected nil content, got %v", got.Content)
		}
	})

	t.Run("no text blocks returns nil content", func(t *testing.T) {
		msg := agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{
			agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}},
		}}
		got := stripNonTextBlocks(msg)
		if got.Content != nil {
			t.Fatalf("expected nil content when no text blocks, got %v", got.Content)
		}
	})

	t.Run("does not mutate input", func(t *testing.T) {
		original := []agent.ContentBlock{
			agent.TextBlock{Text: "keep"},
			agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}},
		}
		msg := agent.Message{Role: agent.RoleUser, Content: original}
		_ = stripNonTextBlocks(msg)
		if len(msg.Content) != 2 {
			t.Fatal("input message was mutated")
		}
	})
}

func TestNewMediaSummaryFunc_RolePreservation(t *testing.T) {
	provider := testutil.NewMockProvider(testutil.WithResponses(
		&agent.ProviderResponse{Text: "described image content"},
	))
	fn := NewMediaSummaryFunc(provider, "describe this")

	msg := agent.Message{
		Role:    agent.RoleUser,
		Content: []agent.ContentBlock{agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}}},
	}
	result, err := fn(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Role != agent.RoleUser {
		t.Fatalf("expected role %q, got %q", agent.RoleUser, result.Role)
	}
}

func TestNewMediaSummaryFunc_SingleTextBlockOutput(t *testing.T) {
	provider := testutil.NewMockProvider(testutil.WithResponses(
		&agent.ProviderResponse{Text: "summary text"},
	))
	fn := NewMediaSummaryFunc(provider, "describe this")

	msg := agent.Message{
		Role:    agent.RoleAssistant,
		Content: []agent.ContentBlock{agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}}},
	}
	result, err := fn(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	tb, ok := result.Content[0].(agent.TextBlock)
	if !ok {
		t.Fatal("expected TextBlock")
	}
	if tb.Text != "summary text" {
		t.Fatalf("expected text %q, got %q", "summary text", tb.Text)
	}
}

func TestNewMediaSummaryFunc_ErrorWrapping(t *testing.T) {
	provider := testutil.NewMockProvider(testutil.WithError(errors.New("provider failed")))
	fn := NewMediaSummaryFunc(provider, "describe this")

	msg := agent.Message{
		Role:    agent.RoleUser,
		Content: []agent.ContentBlock{agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}}},
	}
	_, err := fn(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "media summary:") {
		t.Fatalf("expected error to contain 'media summary:', got %q", err.Error())
	}
}

func TestDefaultMediaSummaryFunc_Works(t *testing.T) {
	provider := testutil.NewMockProvider(testutil.WithResponses(
		&agent.ProviderResponse{Text: "image shows a cat"},
	))
	fn := DefaultMediaSummaryFunc(provider)

	msg := agent.Message{
		Role:    agent.RoleUser,
		Content: []agent.ContentBlock{agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}}},
	}
	result, err := fn(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Role != agent.RoleUser {
		t.Fatalf("expected role %q, got %q", agent.RoleUser, result.Role)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	tb, ok := result.Content[0].(agent.TextBlock)
	if !ok {
		t.Fatal("expected TextBlock")
	}
	if tb.Text != "image shows a cat" {
		t.Fatalf("expected text %q, got %q", "image shows a cat", tb.Text)
	}
}

func TestMediaSummary_IntegrationFlow(t *testing.T) {
	store := NewInMemory()
	ctx := context.Background()

	// Track what the SummaryFunc receives
	var receivedMsgs []agent.Message
	var mu sync.Mutex
	summaryDone := make(chan struct{})

	summaryFn := func(_ context.Context, msgs []agent.Message) ([2]agent.Message, error) {
		mu.Lock()
		receivedMsgs = make([]agent.Message, len(msgs))
		copy(receivedMsgs, msgs)
		mu.Unlock()
		close(summaryDone)
		return [2]agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "summary"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "ok"}}},
		}, nil
	}

	// MediaSummaryFunc that converts images to text descriptions
	mediaFn := func(_ context.Context, msg agent.Message) (agent.Message, error) {
		return agent.Message{
			Role:    msg.Role,
			Content: []agent.ContentBlock{agent.TextBlock{Text: "[image description]"}},
		}, nil
	}

	// threshold=5 turns (10 messages), triggers at 8 messages
	s, err := NewSummary(store, 5, summaryFn, WithMediaSummaryFunc(mediaFn))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create 8 messages, some with images
	msgs := make([]agent.Message, 8)
	for i := range msgs {
		if i%3 == 0 {
			// Every 3rd message has an image
			msgs[i] = agent.Message{
				Role: agent.RoleUser,
				Content: []agent.ContentBlock{
					agent.TextBlock{Text: "look at this"},
					agent.ImageBlock{Source: agent.ImageSource{Base64: "abc", MIMEType: "image/png"}},
				},
			}
		} else {
			msgs[i] = agent.Message{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: fmt.Sprintf("msg %d", i)}},
			}
		}
	}

	if err := s.Save(ctx, "conv", msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Wait for summarization to complete
	select {
	case <-summaryDone:
	case <-time.After(5 * time.Second):
		t.Fatal("summarization did not complete within timeout")
	}

	// Verify the SummaryFunc received text-only messages
	mu.Lock()
	defer mu.Unlock()

	for i, msg := range receivedMsgs {
		for j, block := range msg.Content {
			switch block.(type) {
			case agent.ImageBlock, agent.DocumentBlock:
				t.Fatalf("SummaryFunc received non-text block at msg[%d].Content[%d]: %T", i, j, block)
			}
		}
	}

	// Verify that messages that had images were transformed
	// Messages at indices 0, 3, 6 had images and should now be text-only
	for _, idx := range []int{0, 3, 6} {
		if idx >= len(receivedMsgs) {
			continue
		}
		found := false
		for _, block := range receivedMsgs[idx].Content {
			if tb, ok := block.(agent.TextBlock); ok && tb.Text == "[image description]" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected message %d to contain media summary text '[image description]'", idx)
		}
	}
}
