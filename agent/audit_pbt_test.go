package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

func TestAuditPBT_ContentPolicy(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		message := rapid.String().Draw(rt, "message")
		response := rapid.String().Draw(rt, "response")
		capture := rapid.Bool().Draw(rt, "capture")
		sink := &recordingAuditSink{}
		options := []AuditOption{}
		if capture {
			options = append(options, WithAuditContent())
		}
		a, err := New(newScriptedProvider(&ModelResponse{Text: response}), "sys", WithAudit(sink, options...))
		if err != nil {
			rt.Fatal(err)
		}
		if _, err := a.Invoke(Background(), message); err != nil {
			rt.Fatal(err)
		}
		records := auditRecordsOf[InvokeAuditRecord](sink)
		if len(records) != 2 {
			rt.Fatalf("got %d invoke records", len(records))
		}
		if capture {
			if records[0].UserMessage != message || records[1].Response != response {
				rt.Fatalf("captured content mismatch: %#v", records)
			}
		} else if records[0].UserMessage != "" || records[1].Response != "" {
			rt.Fatalf("redacted content leaked: %#v", records)
		}
	})
}

func TestAuditPBT_ToolMetadataAndContentPolicy(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		callID := rapid.StringMatching(`[a-z0-9]{1,12}`).Draw(rt, "callID")
		output := rapid.String().Draw(rt, "output")
		capture := rapid.Bool().Draw(rt, "capture")
		input, err := json.Marshal(map[string]string{"value": rapid.String().Draw(rt, "value")})
		if err != nil {
			rt.Fatal(err)
		}
		sink := &recordingAuditSink{}
		options := []AuditOption{}
		if capture {
			options = append(options, WithAuditContent())
		}
		provider := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: callID, Name: "echo", Input: input}}},
			&ModelResponse{Text: "done"},
		)
		echo := tool.NewRaw("echo", "echo", nil, func(context.Context, json.RawMessage) (string, error) { return output, nil })
		a, err := New(provider, "sys", WithTools(echo), WithAudit(sink, options...))
		if err != nil {
			rt.Fatal(err)
		}
		if _, err := a.Invoke(Background().WithConversationID("conv"), "go"); err != nil {
			rt.Fatal(err)
		}
		records := auditRecordsOf[AuditRecord](sink)
		if len(records) != 1 || records[0].CallID != callID || records[0].ConversationID != "conv" || !records[0].Allowed {
			rt.Fatalf("tool metadata mismatch: %#v", records)
		}
		if capture {
			if string(records[0].ToolInput) != string(input) || records[0].ToolOutput != output {
				rt.Fatalf("tool content mismatch: %#v", records[0])
			}
		} else if records[0].ToolInput != nil || records[0].ToolOutput != "" {
			rt.Fatalf("tool content leaked: %#v", records[0])
		}
	})
}
