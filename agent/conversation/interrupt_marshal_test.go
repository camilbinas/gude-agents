package conversation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

func TestMarshalInterrupt_ApprovalRoundTrip(t *testing.T) {
	original := &agent.Interrupt{
		ID:             "int-1",
		Type:           agent.InterruptApproval,
		ConversationID: "conv-abc",
		Revision:       7,
		Approval: &agent.ApprovalInterrupt{
			Calls: []agent.ApprovalCall{
				{CallID: "approval-1", Name: "refund", Input: json.RawMessage(`{"amount":500}`)},
				{CallID: "approval-2", Name: "notify", Input: json.RawMessage(`{"channel":"ops"}`)},
			},
		},
		Messages: []agent.Message{
			{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: "I want a refund"}},
			},
			{
				Role: agent.RoleAssistant,
				Content: []agent.ContentBlock{
					agent.ToolUseBlock{ToolUseID: "approval-1", Name: "refund", Input: []byte(`{"amount":500}`)},
					agent.ToolUseBlock{ToolUseID: "approval-2", Name: "notify", Input: []byte(`{"channel":"ops"}`)},
				},
			},
		},
	}

	data, err := MarshalInterrupt(original)
	if err != nil {
		t.Fatalf("MarshalInterrupt: %v", err)
	}
	restored, err := UnmarshalInterrupt(data)
	if err != nil {
		t.Fatalf("UnmarshalInterrupt: %v", err)
	}

	if restored.ID != original.ID {
		t.Errorf("ID = %q, want %q", restored.ID, original.ID)
	}
	if restored.Type != original.Type {
		t.Errorf("Type = %q, want %q", restored.Type, original.Type)
	}
	if restored.ConversationID != original.ConversationID {
		t.Errorf("ConversationID = %q, want %q", restored.ConversationID, original.ConversationID)
	}
	if restored.Revision != original.Revision {
		t.Errorf("Revision = %d, want %d", restored.Revision, original.Revision)
	}
	if !json.Valid(data) || !strings.Contains(string(data), `"revision":7`) {
		t.Errorf("durable JSON does not contain revision %d: %s", original.Revision, data)
	}
	if restored.Input != nil {
		t.Errorf("Input = %+v, want nil", restored.Input)
	}
	if !reflect.DeepEqual(restored.Approval, original.Approval) {
		t.Errorf("Approval = %#v, want %#v", restored.Approval, original.Approval)
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

func TestMarshalInterrupt_HumanInputRoundTrip(t *testing.T) {
	original := &agent.Interrupt{
		ID:             "int-2",
		Type:           agent.InterruptHumanInput,
		ConversationID: "conv-abc",
		Revision:       11,
		Input: &agent.InputInterrupt{
			Reason:   "needs manager approval",
			Question: "Can you approve a $500 refund?",
		},
		Messages: []agent.Message{
			{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{agent.TextBlock{Text: "I want a refund"}},
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
					agent.ToolResultBlock{ToolUseID: "tu-1", Content: "Paused — waiting for human input."},
				},
			},
		},
	}

	data, err := MarshalInterrupt(original)
	if err != nil {
		t.Fatalf("MarshalInterrupt: %v", err)
	}
	restored, err := UnmarshalInterrupt(data)
	if err != nil {
		t.Fatalf("UnmarshalInterrupt: %v", err)
	}

	if restored.Approval != nil {
		t.Errorf("Approval = %+v, want nil", restored.Approval)
	}
	if !reflect.DeepEqual(restored.Input, original.Input) {
		t.Errorf("Input = %+v, want %+v", restored.Input, original.Input)
	}
	if restored.Revision != original.Revision {
		t.Errorf("Revision = %d, want %d", restored.Revision, original.Revision)
	}
	if !reflect.DeepEqual(restored.Messages, original.Messages) {
		t.Errorf("Messages mismatch:\n got  %+v\n want %+v", restored.Messages, original.Messages)
	}
}

func TestMarshalInterrupt_EmptyMessages(t *testing.T) {
	in := &agent.Interrupt{
		ID:             "i",
		Type:           agent.InterruptHumanInput,
		ConversationID: "c",
		Input:          &agent.InputInterrupt{Reason: "r", Question: "q"},
	}

	data, err := MarshalInterrupt(in)
	if err != nil {
		t.Fatalf("MarshalInterrupt: %v", err)
	}
	restored, err := UnmarshalInterrupt(data)
	if err != nil {
		t.Fatalf("UnmarshalInterrupt: %v", err)
	}
	if len(restored.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(restored.Messages))
	}
}

func TestMarshalInterrupt_Nil(t *testing.T) {
	if _, err := MarshalInterrupt(nil); err == nil {
		t.Fatal("expected error for nil interrupt")
	}
}

func TestMarshalInterrupt_AllBlockTypes(t *testing.T) {
	in := &agent.Interrupt{
		ID:   "i",
		Type: agent.InterruptHumanInput,
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

	data, err := MarshalInterrupt(in)
	if err != nil {
		t.Fatalf("MarshalInterrupt: %v", err)
	}
	restored, err := UnmarshalInterrupt(data)
	if err != nil {
		t.Fatalf("UnmarshalInterrupt: %v", err)
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
