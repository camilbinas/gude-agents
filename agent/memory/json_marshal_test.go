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
