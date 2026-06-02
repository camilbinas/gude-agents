package conversation

import (
	"reflect"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

func TestMarshalHandoffRequest_RoundTrip(t *testing.T) {
	original := &agent.HandoffRequest{
		Reason:         "needs manager approval",
		Question:       "Can you approve a $500 refund?",
		ConversationID: "conv-abc",
		Messages: []agent.Message{
			{
				Role: agent.RoleUser,
				Content: []agent.ContentBlock{
					agent.TextBlock{Text: "I want a refund"},
				},
			},
			{
				Role: agent.RoleAssistant,
				Content: []agent.ContentBlock{
					agent.ToolUseBlock{
						ToolUseID: "tu-1",
						Name:      "request_human_input",
						Input:     []byte(`{"reason":"needs approval","question":"Approve?"}`),
					},
				},
			},
			{
				Role: agent.RoleUser,
				Content: []agent.ContentBlock{
					agent.ToolResultBlock{
						ToolUseID: "tu-1",
						Content:   "Paused — waiting for human input.",
					},
				},
			},
		},
	}

	data, err := MarshalHandoffRequest(original)
	if err != nil {
		t.Fatalf("MarshalHandoffRequest: %v", err)
	}

	restored, err := UnmarshalHandoffRequest(data)
	if err != nil {
		t.Fatalf("UnmarshalHandoffRequest: %v", err)
	}

	if restored.Reason != original.Reason {
		t.Errorf("Reason = %q, want %q", restored.Reason, original.Reason)
	}
	if restored.Question != original.Question {
		t.Errorf("Question = %q, want %q", restored.Question, original.Question)
	}
	if restored.ConversationID != original.ConversationID {
		t.Errorf("ConversationID = %q, want %q", restored.ConversationID, original.ConversationID)
	}
	if len(restored.Messages) != len(original.Messages) {
		t.Fatalf("Messages len = %d, want %d", len(restored.Messages), len(original.Messages))
	}

	for i, m := range restored.Messages {
		if m.Role != original.Messages[i].Role {
			t.Errorf("Messages[%d].Role = %q, want %q", i, m.Role, original.Messages[i].Role)
		}
		if !reflect.DeepEqual(m.Content, original.Messages[i].Content) {
			t.Errorf("Messages[%d].Content mismatch:\n got  %+v\n want %+v", i, m.Content, original.Messages[i].Content)
		}
	}
}

func TestMarshalHandoffRequest_EmptyMessages(t *testing.T) {
	hr := &agent.HandoffRequest{
		Reason:         "r",
		Question:       "q",
		ConversationID: "c",
		Messages:       nil,
	}

	data, err := MarshalHandoffRequest(hr)
	if err != nil {
		t.Fatalf("MarshalHandoffRequest: %v", err)
	}

	restored, err := UnmarshalHandoffRequest(data)
	if err != nil {
		t.Fatalf("UnmarshalHandoffRequest: %v", err)
	}

	if len(restored.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(restored.Messages))
	}
}

func TestMarshalHandoffRequest_AllBlockTypes(t *testing.T) {
	hr := &agent.HandoffRequest{
		Reason:         "r",
		Question:       "q",
		ConversationID: "c",
		Messages: []agent.Message{
			{
				Role: agent.RoleUser,
				Content: []agent.ContentBlock{
					agent.TextBlock{Text: "hello"},
					agent.ImageBlock{Source: agent.ImageSource{Base64: "abc123", MIMEType: "image/png"}},
					agent.WidgetBlock{Type: "chart", Payload: []byte(`{"title":"Q1"}`)},
				},
			},
		},
	}

	data, err := MarshalHandoffRequest(hr)
	if err != nil {
		t.Fatalf("MarshalHandoffRequest: %v", err)
	}

	restored, err := UnmarshalHandoffRequest(data)
	if err != nil {
		t.Fatalf("UnmarshalHandoffRequest: %v", err)
	}

	if len(restored.Messages[0].Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(restored.Messages[0].Content))
	}

	tb, ok := restored.Messages[0].Content[0].(agent.TextBlock)
	if !ok || tb.Text != "hello" {
		t.Errorf("block 0: expected TextBlock{hello}, got %T %+v", restored.Messages[0].Content[0], restored.Messages[0].Content[0])
	}

	ib, ok := restored.Messages[0].Content[1].(agent.ImageBlock)
	if !ok || ib.Source.Base64 != "abc123" {
		t.Errorf("block 1: expected ImageBlock{abc123}, got %T %+v", restored.Messages[0].Content[1], restored.Messages[0].Content[1])
	}

	wb, ok := restored.Messages[0].Content[2].(agent.WidgetBlock)
	if !ok || wb.Type != "chart" {
		t.Errorf("block 2: expected WidgetBlock{chart}, got %T %+v", restored.Messages[0].Content[2], restored.Messages[0].Content[2])
	}
}
