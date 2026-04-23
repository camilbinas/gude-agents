package memory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// TestMarshal_TextBlock serialises a message containing a TextBlock and asserts
// the JSON output contains the expected "type":"text" and "text" fields.
// Validates: Req 13.1
func TestMarshal_TextBlock(t *testing.T) {
	messages := []agent.Message{
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{
				agent.TextBlock{Text: "hello world"},
			},
		},
	}

	data, err := MarshalMessages(messages)
	if err != nil {
		t.Fatalf("MarshalMessages returned unexpected error: %v", err)
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"type":"text"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"type":"text"`, s)
	}
	if !strings.Contains(s, `"text"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"text"`, s)
	}
	if !strings.Contains(s, "hello world") {
		t.Errorf("expected JSON to contain text value %q, got: %s", "hello world", s)
	}
}

// TestMarshal_ToolUseBlock serialises a message containing a ToolUseBlock and
// asserts the JSON output contains the expected fields.
// Validates: Req 13.2
func TestMarshal_ToolUseBlock(t *testing.T) {
	messages := []agent.Message{
		{
			Role: agent.RoleAssistant,
			Content: []agent.ContentBlock{
				agent.ToolUseBlock{
					ToolUseID: "tu_abc123",
					Name:      "get_weather",
					Input:     json.RawMessage(`{"city":"London"}`),
				},
			},
		},
	}

	data, err := MarshalMessages(messages)
	if err != nil {
		t.Fatalf("MarshalMessages returned unexpected error: %v", err)
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"type":"tool_use"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"type":"tool_use"`, s)
	}
	if !strings.Contains(s, `"tool_use_id"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"tool_use_id"`, s)
	}
	if !strings.Contains(s, `"name"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"name"`, s)
	}
	if !strings.Contains(s, `"input"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"input"`, s)
	}
}

// TestMarshal_ToolResultBlock serialises a message containing a ToolResultBlock
// and asserts the JSON output contains the expected fields.
// Validates: Req 13.3
func TestMarshal_ToolResultBlock(t *testing.T) {
	messages := []agent.Message{
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{
				agent.ToolResultBlock{
					ToolUseID: "tu_abc123",
					Content:   "sunny, 22°C",
					IsError:   false,
				},
			},
		},
	}

	data, err := MarshalMessages(messages)
	if err != nil {
		t.Fatalf("MarshalMessages returned unexpected error: %v", err)
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"type":"tool_result"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"type":"tool_result"`, s)
	}
	if !strings.Contains(s, `"tool_use_id"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"tool_use_id"`, s)
	}
	if !strings.Contains(s, `"content"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"content"`, s)
	}

	// is_error is omitempty so it only appears when true.
	messagesWithError := []agent.Message{
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{
				agent.ToolResultBlock{
					ToolUseID: "tu_err1",
					Content:   "something went wrong",
					IsError:   true,
				},
			},
		},
	}
	dataErr, err := MarshalMessages(messagesWithError)
	if err != nil {
		t.Fatalf("MarshalMessages (IsError=true) returned unexpected error: %v", err)
	}
	if !strings.Contains(string(dataErr), `"is_error"`) {
		t.Errorf("expected JSON to contain %q when IsError=true, got: %s", `"is_error"`, string(dataErr))
	}
}

// TestUnmarshal_EmptyArray verifies that UnmarshalMessages on an empty JSON array
// returns a non-nil empty slice and a nil error.
func TestUnmarshal_EmptyArray(t *testing.T) {
	messages, err := UnmarshalMessages([]byte("[]"))
	if err != nil {
		t.Fatalf("UnmarshalMessages returned unexpected error: %v", err)
	}
	if messages == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(messages) != 0 {
		t.Fatalf("expected empty slice, got %d messages", len(messages))
	}
}

// TestMarshalUnmarshal_ImageBlock_RawBytes serialises a message containing
// an ImageBlock with raw bytes and verifies the round-trip preserves the
// data, base64, and MIME type fields.
func TestMarshalUnmarshal_ImageBlock_RawBytes(t *testing.T) {
	rawBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	original := []agent.Message{
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{
				agent.ImageBlock{
					Source: agent.ImageSource{
						Data:     rawBytes,
						MIMEType: "image/jpeg",
					},
				},
				agent.TextBlock{Text: "describe this"},
			},
		},
	}

	data, err := MarshalMessages(original)
	if err != nil {
		t.Fatalf("MarshalMessages: %v", err)
	}

	if !strings.Contains(string(data), `"type":"image"`) {
		t.Errorf("expected JSON to contain %q, got: %s", `"type":"image"`, string(data))
	}

	got, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if len(got[0].Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(got[0].Content))
	}

	img, ok := got[0].Content[0].(agent.ImageBlock)
	if !ok {
		t.Fatalf("expected content[0] to be ImageBlock, got %T", got[0].Content[0])
	}
	if string(img.Source.Data) != string(rawBytes) {
		t.Errorf("Data: expected %v, got %v", rawBytes, img.Source.Data)
	}
	if img.Source.Base64 != "" {
		t.Errorf("Base64: expected empty, got %q", img.Source.Base64)
	}
	if img.Source.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType: expected %q, got %q", "image/jpeg", img.Source.MIMEType)
	}

	tb, ok := got[0].Content[1].(agent.TextBlock)
	if !ok {
		t.Fatalf("expected content[1] to be TextBlock, got %T", got[0].Content[1])
	}
	if tb.Text != "describe this" {
		t.Errorf("TextBlock.Text: expected %q, got %q", "describe this", tb.Text)
	}
}

// TestMarshalUnmarshal_ImageBlock_Base64 verifies the round-trip preserves
// a pre-encoded base64 source without corrupting or re-encoding it.
func TestMarshalUnmarshal_ImageBlock_Base64(t *testing.T) {
	const preEncoded = "aGVsbG8gaW1hZ2UgZGF0YQ=="
	original := []agent.Message{
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{
				agent.ImageBlock{
					Source: agent.ImageSource{
						Base64:   preEncoded,
						MIMEType: "image/png",
					},
				},
			},
		},
	}

	data, err := MarshalMessages(original)
	if err != nil {
		t.Fatalf("MarshalMessages: %v", err)
	}

	got, err := UnmarshalMessages(data)
	if err != nil {
		t.Fatalf("UnmarshalMessages: %v", err)
	}

	img, ok := got[0].Content[0].(agent.ImageBlock)
	if !ok {
		t.Fatalf("expected ImageBlock, got %T", got[0].Content[0])
	}
	if img.Source.Base64 != preEncoded {
		t.Errorf("Base64: expected %q, got %q", preEncoded, img.Source.Base64)
	}
	if len(img.Source.Data) != 0 {
		t.Errorf("Data: expected empty, got %v", img.Source.Data)
	}
	if img.Source.MIMEType != "image/png" {
		t.Errorf("MIMEType: expected %q, got %q", "image/png", img.Source.MIMEType)
	}
}
